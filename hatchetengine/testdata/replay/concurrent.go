package main

import (
	"context"
	"controls/execution"
	"controls/hatchetengine"
	"controls/internal/comms"
	"encoding/json"
	"fmt"
	"github.com/a2aproject/a2a-go/a2a"
	"github.com/google/uuid"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
	"time"
)

type authenticated struct{ token string }

func (a authenticated) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(r)
}
func (r *runner) collaborate(ctx context.Context, work execution.Work) (execution.StepResult, error) {
	connection, err := r.communication.Provision(ctx, work)
	if err != nil {
		return execution.StepResult{}, err
	}
	defer r.communication.End(context.Background(), connection.ParticipantID)
	if connection.Result != nil {
		return *connection.Result, nil
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "deterministic-worker", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: connection.URL, HTTPClient: &http.Client{Transport: authenticated{connection.Token}}}, nil)
	if err != nil {
		return execution.StepResult{}, err
	}
	defer session.Close()
	call := func(name string, args any, out any) error {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return err
		}
		if result.IsError {
			return fmt.Errorf("MCP %s rejected: %v", name, result.Content)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return err
		}
		if out != nil {
			return json.Unmarshal(raw, out)
		}
		return nil
	}
	if err = call("send_message", comms.Send{OperationID: "hello", ThreadID: connection.ThreadID, Payload: comms.Structured("message", map[string]any{"step": work.Step.ID, "round": work.Visit, "attempt": work.Try})}, nil); err != nil {
		return execution.StepResult{}, err
	}
	cursor := ""
	seenPeer := false
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for !seenPeer {
		if err = waitCtx.Err(); err != nil {
			return execution.StepResult{}, fmt.Errorf("peer was not running concurrently: %w", err)
		}
		var batch comms.Batch
		if err = call("wait_for_message", comms.Read{ThreadID: connection.ThreadID, After: cursor, WaitSeconds: 1}, &batch); err != nil {
			return execution.StepResult{}, err
		}
		cursor = batch.Cursor
		for _, message := range batch.Messages {
			if message.Sender != connection.ParticipantID && message.Payload.Kind == "message" {
				for _, part := range message.Payload.Parts {
					if data, ok := part.(a2a.DataPart); ok {
						seenPeer = data.Data["attempt"] == float64(work.Try) && data.Data["round"] == float64(work.Visit)
					}
				}
			}
		}
	}
	if work.Brief == "fail once" && work.Step.ID == "review" && work.Try == 1 {
		return execution.StepResult{}, fmt.Errorf("deliberate fixture failure")
	}
	outcome := "ready"
	if work.Brief != "fail once" && work.Visit == 1 && work.Step.ID == "review" {
		outcome = "revise"
	}
	if err = call("send_message", comms.Send{OperationID: "result", ThreadID: connection.ThreadID, Payload: comms.Structured("step_result", map[string]any{"outcome": outcome, "summary": "Exchanged peer feedback through MCP"})}, nil); err != nil {
		return execution.StepResult{}, err
	}
	result, err := r.communication.Result(ctx, connection.ParticipantID)
	if err != nil {
		return execution.StepResult{}, err
	}
	if result == nil {
		return execution.StepResult{}, fmt.Errorf("result was not durable")
	}
	return *result, nil
}
func checkConcurrent(ctx context.Context, engine *hatchetengine.Engine, progress <-chan hatchetengine.Progress, r *runner, client *hatchet.Client) error {
	config := execution.Configuration{Workflow: execution.Workflow{Name: "concurrent-test", Start: "build", Stages: []execution.Stage{{ID: "pair", Steps: []execution.StepID{"build", "review"}, Loop: &execution.LoopPolicy{Mode: "concurrent", Limit: 2}}}}, Packages: []execution.PackageSnapshot{{Name: "worker", Digest: "fixture", Runner: "test"}}}
	for _, id := range []execution.StepID{"build", "review"} {
		config.Workflow.Steps = append(config.Workflow.Steps, execution.Step{ID: id, Stage: "pair", Kind: execution.RunStep, Run: &execution.RunSpec{Package: "worker"}, Routes: map[execution.Outcome]execution.StepID{"ready": execution.Complete, "revise": "build"}})
	}
	started, err := engine.Start(ctx, execution.StartRequest{ExecutionID: uuid.NewString(), Project: "/tmp", Actor: "test", RequestID: uuid.NewString(), Configuration: config})
	if err != nil {
		return err
	}
	if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
		return fmt.Errorf("concurrent loop: %w", err)
	}
	if got := r.calls.Load(); got != 5 {
		return fmt.Errorf("expected original worker and two concurrent rounds (5 invocations), got %d", got)
	}
	if err = waitForCommittedRun(ctx, client, started.RunID); err != nil {
		return fmt.Errorf("before concurrent replay: %w", err)
	}
	runID, err := uuid.Parse(started.RunID)
	if err != nil {
		return err
	}
	ids := []openapi_types.UUID{openapi_types.UUID(runID)}
	if _, err = client.Runs().Replay(ctx, rest.V1ReplayTaskRequest{ExternalIds: &ids}); err != nil {
		return err
	}
	if err = waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
		return err
	}
	if err = waitForCommittedRun(ctx, client, started.RunID); err != nil {
		return fmt.Errorf("after concurrent replay: %w", err)
	}
	if r.calls.Load() != 5 {
		return fmt.Errorf("concurrent replay launched duplicate workers")
	}
	retried, err := engine.Start(ctx, execution.StartRequest{ExecutionID: uuid.NewString(), Project: "/tmp", Actor: "test", RequestID: uuid.NewString(), Brief: "fail once", Configuration: config})
	if err != nil {
		return err
	}
	if err = waitForProgress(ctx, progress, "step_blocked", "review"); err != nil {
		return err
	}
	if err = engine.SendRetry(ctx, execution.Retry{ExecutionID: retried.ExecutionID, Step: "review", Visit: 1, Try: 1, Actor: "test", Reason: "retry interrupted round"}); err != nil {
		return err
	}
	if err = waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
		return err
	}
	if r.calls.Load() != 9 {
		return fmt.Errorf("round retry did not restart both members: %d calls", r.calls.Load())
	}
	fmt.Println("CONCURRENT_MCP_OK")
	return nil
}
