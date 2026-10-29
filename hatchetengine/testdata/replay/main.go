package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"controls/execution"
	"controls/hatchetengine"

	"controls/internal/comms"
	"controls/internal/state"
	"github.com/google/uuid"
	embed "github.com/hatchet-dev/hatchet-embedded"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type runner struct {
	calls         atomic.Int32
	communication *comms.Service
}

func (r *runner) Run(ctx context.Context, work hatchetengine.Work) (execution.StepResult, error) {
	r.calls.Add(1)
	if len(work.Peers) > 0 {
		return r.collaborate(ctx, work)
	}
	return execution.StepResult{Outcome: "done", Summary: "done", Actor: "test-runner", At: time.Now().UTC()}, nil
}

type observer struct{ progress chan hatchetengine.Progress }

func (o observer) Record(_ context.Context, progress hatchetengine.Progress) error {
	if progress.Kind == "execution_completed" || progress.Kind == "decision_requested" || progress.Kind == "step_blocked" || progress.Kind == "stage_round_interrupted" {
		fmt.Printf("OBSERVED %s execution=%s step=%s attempt=%d\n", progress.Kind, progress.ExecutionID, progress.Step.ID, progress.Try)
	}
	o.progress <- progress
	return nil
}

func waitForProgress(ctx context.Context, progress <-chan hatchetengine.Progress, kind string, step execution.StepID) error {
	for {
		select {
		case item := <-progress:
			if item.Kind == "execution_failed" {
				return fmt.Errorf("durable execution failed: %s", item.Error)
			}
			if item.Kind == kind && item.Step.ID == step {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s on %s: %w", kind, step, ctx.Err())
		}
	}
}

func workflow() execution.Configuration {
	return execution.Configuration{
		Workflow: execution.Workflow{Name: "replay-test", Start: "design", Steps: []execution.Step{
			{ID: "design", Kind: execution.DecisionStep,
				Decision: &execution.DecisionSpec{Authority: "human", Options: []execution.Outcome{"approve"}},
				Routes:   map[execution.Outcome]execution.StepID{"approve": "build"}},
			{ID: "build", Kind: execution.RunStep, Run: &execution.RunSpec{Package: "builder"},
				Routes: map[execution.Outcome]execution.StepID{"done": "acceptance"}},
			{ID: "acceptance", Kind: execution.DecisionStep,
				Decision: &execution.DecisionSpec{Authority: "human", Options: []execution.Outcome{"approve"}},
				Routes:   map[execution.Outcome]execution.StepID{"approve": execution.Complete}},
		}},
		Packages: []execution.PackageSnapshot{{Name: "builder", Digest: "test-package", Runner: "test"}},
	}
}

func run(ctx context.Context) (runErr error) {
	directory, err := os.MkdirTemp("", "factory-engine-integration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	instance, err := embed.Start(ctx, embed.WithPostgresDataDir(directory))
	var client *hatchet.Client
	if err == nil {
		client = instance.Client()
	}
	if err != nil {
		return fmt.Errorf("start embedded Hatchet: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := instance.Shutdown(stopCtx); runErr == nil && err != nil {
			runErr = fmt.Errorf("close embedded Hatchet: %w", err)
		}
	}()

	database, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer database.Close()
	communication, err := comms.New(ctx, database, nil)
	if err != nil {
		return err
	}
	stopComms, err := communication.Listen(ctx)
	if err != nil {
		return err
	}
	defer stopComms()
	packageRunner := &runner{communication: communication}
	progress := make(chan hatchetengine.Progress, 256)
	engine, err := hatchetengine.New(client, packageRunner, observer{progress: progress})
	if err != nil {
		return err
	}
	worker, err := engine.NewWorker("factory-replay-test")
	if err != nil {
		return err
	}
	stop, err := worker.Start()
	if err != nil {
		return err
	}
	defer func() {
		if err := stop(); runErr == nil && err != nil {
			runErr = fmt.Errorf("stop worker: %w", err)
		}
	}()

	started, err := engine.Start(ctx, execution.StartRequest{
		ExecutionID: uuid.NewString(), Project: os.TempDir(), Actor: "test", RequestID: uuid.NewString(), Configuration: workflow(),
	})
	if err != nil {
		return err
	}
	if err := waitForProgress(ctx, progress, "decision_requested", "design"); err != nil {
		return err
	}
	if err := engine.SendDecision(ctx, hatchetengine.Decision{
		ExecutionID: started.ExecutionID, Step: "design", Visit: 1, Outcome: "approve",
		Actor: "human", Reason: "approved design", At: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := waitForProgress(ctx, progress, "decision_requested", "acceptance"); err != nil {
		return err
	}
	if got := packageRunner.calls.Load(); got != 1 {
		return fmt.Errorf("builder ran %d times before replay, want one", got)
	}
	if err := engine.SendDecision(ctx, hatchetengine.Decision{
		ExecutionID: started.ExecutionID, Step: "acceptance", Visit: 1, Outcome: "approve",
		Actor: "human", Reason: "accepted", At: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
		return err
	}

	if err = waitForCommittedRun(ctx, client, started.RunID); err != nil {
		return fmt.Errorf("before human replay: %w", err)
	}
	runID, err := uuid.Parse(started.RunID)
	if err != nil {
		return err
	}
	ids := []openapi_types.UUID{openapi_types.UUID(runID)}
	if _, err := client.Runs().Replay(ctx, rest.V1ReplayTaskRequest{ExternalIds: &ids}); err != nil {
		return fmt.Errorf("force durable replay: %w", err)
	}
	if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
		return err
	}
	if err = waitForCommittedRun(ctx, client, started.RunID); err != nil {
		return fmt.Errorf("after human replay: %w", err)
	}
	if got := packageRunner.calls.Load(); got != 1 {
		return fmt.Errorf("replay ran the builder %d times, want one persisted result", got)
	}
	return checkConcurrent(ctx, engine, progress, packageRunner, client)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("REPLAY_OK")
}

// The observer fires inside the durable task, before the engine commits its
// terminal state. Administrative replay must wait for that commit explicitly.
func waitForCommittedRun(ctx context.Context, client *hatchet.Client, id string) error {
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := client.Runs().GetStatus(wait, id)
		if err == nil && status != nil {
			if *status == rest.V1TaskStatusCOMPLETED {
				return nil
			}
			if string(*status) == "FAILED" || string(*status) == "CANCELLED" {
				return fmt.Errorf("run %s is %s", id, *status)
			}
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("waiting for committed run %s (last status %v, error %v): %w", id, status, err, wait.Err())
		case <-ticker.C:
		}
	}
}
