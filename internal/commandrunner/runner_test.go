package commandrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controls/execution"
	"controls/internal/comms"
	"controls/internal/factoryconfig"
	"controls/internal/sessions"

	"github.com/stretchr/testify/require"
)

type testCommunication struct {
	connection comms.Connection
	result     *execution.StepResult
	ended      bool
	recover    func() error
}

func (c *testCommunication) Provision(context.Context, execution.Work) (comms.Connection, error) {
	return c.connection, nil
}
func (c *testCommunication) BindSession(context.Context, string, string) error { return nil }
func (c *testCommunication) Result(context.Context, string) (*execution.StepResult, error) {
	return c.result, nil
}
func (c *testCommunication) Recover(context.Context, string) error {
	if c.recover != nil {
		return c.recover()
	}
	return nil
}
func (c *testCommunication) End(context.Context, string) error { c.ended = true; return nil }

type testSessionHost struct {
	launch func(sessions.Launch)
	calls  int
	output string
	get    func() (sessions.Session, error)
	stops  int
}

func (h *testSessionHost) Launch(args sessions.Launch) (sessions.Session, error) {
	h.calls++
	h.launch(args)
	return sessions.Session{ID: "session", State: sessions.Exited}, nil
}
func (h *testSessionHost) Get(string) (sessions.Session, error) {
	if h.get != nil {
		return h.get()
	}
	return sessions.Session{State: sessions.Exited}, nil
}
func (h *testSessionHost) Stop(string) error                       { h.stops++; return nil }
func (*testSessionHost) ReportBudget(string, string, string) error { return nil }
func (h *testSessionHost) Output(string, int64, int) (sessions.Output, error) {
	if h.output != "" {
		return sessions.Output{Text: h.output}, nil
	}
	return sessions.Output{Text: "process output"}, nil
}

type testRecorder struct{}

func (testRecorder) AttachSession(string, string, string) error { return nil }

func fixture(t *testing.T) execution.Work {
	t.Helper()
	root := t.TempDir()
	agent := filepath.Join(root, "agent")
	require.NoError(t, os.Mkdir(agent, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(agent, "run.sh"), []byte("#!/bin/sh\n"), 0700))
	config := `{"name":"fixture","agents":[{"name":"script","path":"agent","entrypoint":"run.sh","args":["{prompt}"]}],"workflow":{"start":"work","steps":[{"name":"work","type":"agent","agent":"script","on":{"ready":"$done"}}]}}`
	path := filepath.Join(root, "factory.json")
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))
	resolved, err := factoryconfig.Load(path)
	require.NoError(t, err)
	return execution.Work{ExecutionID: "execution", Project: root, Step: resolved.Configuration.Workflow.Steps[0], Package: resolved.Configuration.Packages[0], Visit: 1, Try: 1}
}
func TestCommandPackageRequiresAcceptedMCPResult(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(map[bool]string{true: "accepted result", false: "exit without acceptance"}[accepted], func(t *testing.T) {
			work := fixture(t)
			communication := &testCommunication{connection: comms.Connection{New: true, URL: "http://127.0.0.1:1234/mcp", Token: "private", ThreadID: "thread", ParticipantID: "participant"}}
			host := &testSessionHost{launch: func(args sessions.Launch) {
				require.Equal(t, "private", args.Environment["FACTORY_MCP_TOKEN"])
				require.Empty(t, args.Environment["FACTORY_RESULT_PATH"])
				skill, err := os.ReadFile(filepath.Join(args.Environment["FACTORY_COMMS_SKILL"], "SKILL.md"))
				require.NoError(t, err)
				require.Contains(t, string(skill), "send_message")
				if accepted {
					communication.result = &execution.StepResult{Outcome: "ready", Summary: "done", Actor: "participant", At: time.Now().UTC()}
				}
			}}
			result, err := (Runner{Sessions: host, Recorder: testRecorder{}, Comms: communication, Root: t.TempDir()}).Run(context.Background(), work)
			if accepted {
				require.NoError(t, err)
				require.Equal(t, execution.Outcome("ready"), result.Outcome)
			} else {
				require.ErrorContains(t, err, "without an accepted result")
			}
			require.True(t, communication.ended)
			require.Equal(t, 1, host.calls)
		})
	}
}
func TestPackageChangeIsRejected(t *testing.T) {
	work := fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(work.Package.Source, "run.sh"), []byte("changed"), 0700))
	communication := &testCommunication{connection: comms.Connection{New: true, ParticipantID: "worker"}}
	_, err := (Runner{Comms: communication}).Run(context.Background(), work)
	require.ErrorContains(t, err, "changed since execution began")
}
func TestReplayRecoversAcceptedResultWithoutWorkerLaunch(t *testing.T) {
	work := fixture(t)
	result := &execution.StepResult{Outcome: "ready", Summary: "already accepted", Actor: "worker", At: time.Now().UTC()}
	communication := &testCommunication{connection: comms.Connection{ParticipantID: "worker", Result: result}}
	actual, err := (Runner{Comms: communication}).Run(context.Background(), work)
	require.NoError(t, err)
	require.Equal(t, *result, actual)
	raw, err := json.Marshal(actual)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private")
}

func TestPromptIncludesReviewerArtifactsForNextRound(t *testing.T) {
	prompt := buildPrompt(execution.Work{
		Step: execution.Step{ID: "build", Routes: map[execution.Outcome]execution.StepID{"done": execution.Complete}},
		History: []execution.CompletedStep{{Step: "review", Visit: 2, Result: execution.StepResult{
			Outcome: "changes_requested", Summary: "Handle empty input",
			Artifacts: []execution.Artifact{{Name: "reviews/code.md", URI: "/project/reviews/code.md", Digest: "abc"}},
		}}},
	}, "assigned-thread")
	require.Contains(t, prompt, `"thread_id": "assigned-thread"`)
	require.Contains(t, prompt, `"kind": "step_result"`)
	require.Contains(t, prompt, `"outcome": "done"`)
	require.Contains(t, prompt, "effect.state=accepted")
	require.Contains(t, prompt, "not retried by automatic incoming-message delivery")
	if !strings.Contains(prompt, "review visit 2: changes_requested") || !strings.Contains(prompt, "reviews/code.md: /project/reviews/code.md (sha256 abc)") {
		t.Fatalf("review findings missing from next-round prompt: %s", prompt)
	}
}

func TestAdapterFailureCauseReachesBlockedAttempt(t *testing.T) {
	work := fixture(t)
	communication := &testCommunication{connection: comms.Connection{New: true, ParticipantID: "worker"}}
	host := &testSessionHost{launch: func(sessions.Launch) {}, output: "Factory agent blocked: model response truncated (stopReason=length); continuation limit 2 exhausted"}
	_, err := (Runner{Sessions: host, Recorder: testRecorder{}, Comms: communication, Root: t.TempDir()}).Run(context.Background(), work)
	require.ErrorContains(t, err, "stopReason=length")
	require.ErrorContains(t, err, "continuation limit 2 exhausted")
	require.True(t, communication.ended)
}

func TestBoundJSONAndPackageSettingsReachTheWorker(t *testing.T) {
	work := fixture(t)
	work.Step.InputBindings = map[string]execution.InputBinding{"task": {Execution: "input"}, "evaluation_response": {Step: "evaluate", Output: "response"}}
	work.Brief = "Investigate a timeout"
	work.History = []execution.CompletedStep{{Step: "evaluate", Visit: 1, Result: execution.StepResult{Data: map[string]json.RawMessage{"response": json.RawMessage(`{"answers":{"readiness":{"choice":"clarify"}}}`)}}}}
	work.Package.Settings = json.RawMessage(`{"entrypoint":"run.sh","overrides":{"model":"pinned","api_key_env":"MY_KEY"}}`)
	communication := &testCommunication{connection: comms.Connection{New: true, URL: "http://localhost/mcp", ThreadID: "thread", ParticipantID: "worker"}}
	host := &testSessionHost{launch: func(args sessions.Launch) {
		var values map[string]json.RawMessage
		raw, err := os.ReadFile(args.Environment["FACTORY_INPUTS_PATH"])
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &values))
		require.JSONEq(t, `{"answers":{"readiness":{"choice":"clarify"}}}`, string(values["evaluation_response"]))
		raw, err = os.ReadFile(args.Environment["FACTORY_PACKAGE_CONFIG"])
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"pinned","api_key_env":"MY_KEY"}`, string(raw))
		prompt, err := os.ReadFile(args.Environment["FACTORY_PROMPT_PATH"])
		require.NoError(t, err)
		require.Contains(t, string(prompt), "evaluation_response")
		require.Contains(t, string(prompt), "clarify")
		require.Equal(t, work.Project, args.Directory)
		communication.result = &execution.StepResult{Outcome: "ready", Summary: "done"}
	}}
	_, err := (Runner{Sessions: host, Recorder: testRecorder{}, Comms: communication, Root: t.TempDir()}).Run(context.Background(), work)
	require.NoError(t, err)
}

func TestCompletionRecoveryExhaustionStopsTheSession(t *testing.T) {
	work := fixture(t)
	communication := &testCommunication{connection: comms.Connection{New: true, ThreadID: "thread", ParticipantID: "participant"}, recover: func() error {
		return fmt.Errorf("completion recovery exhausted: no accepted result after 2 controller reminders")
	}}
	host := &testSessionHost{launch: func(sessions.Launch) {}, get: func() (sessions.Session, error) { return sessions.Session{State: sessions.Running}, nil }}
	_, err := (Runner{Sessions: host, Recorder: testRecorder{}, Comms: communication, Root: t.TempDir()}).Run(context.Background(), work)
	require.ErrorContains(t, err, "completion recovery exhausted")
	require.Equal(t, 1, host.stops)
	require.True(t, communication.ended)
}
