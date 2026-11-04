package commandrunner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"controls/adapters"
	"controls/adapters/conformance"
	"controls/execution"
	"controls/internal/artifacts"
	"controls/internal/comms"
	"controls/internal/factoryconfig"
	"controls/internal/sessions"
	"controls/internal/testdb"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// This exercises real preparation, the process supervisor, PostgreSQL and MCP.
// No Pi/Node process, native Pi event fixture, model, or active service is used.
func TestRuntimeConformance(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "worker")
	build := exec.Command("go", "build", "-o", binary, "../../adapters/conformance/worker")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	worker, err := os.ReadFile(binary)
	require.NoError(t, err)
	db := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	manager, err := sessions.Open(t.TempDir(), "", db)
	require.NoError(t, err)
	communication, err := comms.New(ctx, db, artifacts.HostCollector{Root: t.TempDir()})
	require.NoError(t, err)
	stop, err := communication.Listen(ctx)
	require.NoError(t, err)
	defer stop()
	runner := Runner{Adapters: adapters.Registry{"command": adapters.Command{}, "fixture": conformance.Adapter{}}, Sessions: manager, Comms: communication, Recorder: testRecorder{}, Root: t.TempDir()}
	workFor := func(t *testing.T, mode string) execution.Work {
		project := t.TempDir()
		source := filepath.Join(project, "agent")
		require.NoError(t, os.Mkdir(source, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(source, "worker"), worker, 0700))
		definition := map[string]any{"name": "conformance", "packages": []any{map[string]any{"name": "worker", "path": "agent", "entrypoint": "worker", "runtime": map[string]any{"name": "fixture", "version": "=1.0.0"}, "with": map[string]any{"mode": mode, "message": "configured fixture"}}}, "workflow": map[string]any{"start": "work", "steps": []any{map[string]any{"name": "work", "type": "run", "package": "worker", "on": map[string]string{"ready": "$done"}}}}}
		raw, err := json.Marshal(definition)
		require.NoError(t, err)
		file := filepath.Join(project, "factory.json")
		require.NoError(t, os.WriteFile(file, raw, 0600))
		resolved, err := factoryconfig.Load(file)
		require.NoError(t, err)
		return execution.Work{ExecutionID: uuid.NewString(), Project: project, Package: resolved.Configuration.Packages[0], Step: resolved.Configuration.Workflow.Steps[0], Visit: 1, Try: 1}
	}
	t.Run("accepted MCP result and persisted native identity", func(t *testing.T) {
		work := workFor(t, "complete")
		work.Brief = "A bounded fixture assignment"
		work.Step.InputBindings = map[string]execution.InputBinding{"task": {Execution: "input"}}
		result, err := runner.Run(ctx, work)
		require.NoError(t, err)
		require.Equal(t, "configured fixture", result.Summary)
		list, err := manager.List(work.ExecutionID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, "command", list[0].Runner)
		require.Equal(t, "fixture", list[0].Runtime.Name)
		require.Equal(t, "1.0.0", list[0].Runtime.Version)
		require.NotEqual(t, sessions.Running, list[0].State)
		require.Contains(t, string(result.Data["invocation_dir"]), work.ExecutionID)
		require.Contains(t, string(result.Data["inputs"]), "A bounded fixture assignment")
		project, err := filepath.EvalSymlinks(work.Project)
		require.NoError(t, err)
		cwd, err := json.Marshal(project)
		require.NoError(t, err)
		require.JSONEq(t, string(cwd), string(result.Data["working_directory"]))
	})
	t.Run("native failure cannot become completion", func(t *testing.T) {
		_, err := runner.Run(ctx, workFor(t, "fail"))
		require.ErrorContains(t, err, "fixture native failure")
	})
	t.Run("cancellation stops the actual process", func(t *testing.T) {
		work := workFor(t, "wait")
		runCtx, stopRun := context.WithCancel(ctx)
		defer stopRun()
		done := make(chan error, 1)
		go func() { _, err := runner.Run(runCtx, work); done <- err }()
		var sessionID string
		require.Eventually(t, func() bool {
			list, err := manager.List(work.ExecutionID)
			if err != nil || len(list) == 0 {
				return false
			}
			sessionID = list[0].ID
			return list[0].State == sessions.Running
		}, 5*time.Second, 20*time.Millisecond)
		_, err := manager.Send(sessionID, "not supported")
		require.ErrorContains(t, err, "does not accept interactive input")
		stopRun()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("cancellation did not stop invocation")
		}
		require.Eventually(t, func() bool {
			session, err := manager.Get(sessionID)
			return err == nil && session.State != sessions.Running && session.State != sessions.Stopping
		}, 5*time.Second, 20*time.Millisecond)
	})
	t.Run("concurrent peer messages and isolated configuration", func(t *testing.T) {
		author := workFor(t, "peer")
		author.Step.Stage = "pair"
		critic := author
		critic.Step.ID = "critic"
		critic.Package.Name = "critic"
		critic.Package.Settings = json.RawMessage(`{"entrypoint":"worker","with":{"mode":"peer","message":"critic configuration"}}`)
		peers := []execution.Peer{{Step: author.Step.ID, Name: author.Package.Name, Visit: 1, Try: 1}, {Step: critic.Step.ID, Name: critic.Package.Name, Visit: 1, Try: 1}}
		author.Peers = peers
		critic.Peers = peers
		type answer struct {
			result execution.StepResult
			err    error
		}
		answers := make(chan answer, 2)
		for _, work := range []execution.Work{author, critic} {
			go func() { result, err := runner.Run(ctx, work); answers <- answer{result, err} }()
		}
		first, second := <-answers, <-answers
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.ElementsMatch(t, []string{"configured fixture", "critic configuration"}, []string{first.result.Summary, second.result.Summary})
		require.NotEqual(t, string(first.result.Data["invocation_dir"]), string(second.result.Data["invocation_dir"]))
		list, err := manager.List(author.ExecutionID)
		require.NoError(t, err)
		require.Len(t, list, 2)
	})
	t.Run("unsupported concurrent capability rejected without launch", func(t *testing.T) {
		work := workFor(t, "complete")
		work.Package.Runtime = nil
		work.Peers = []execution.Peer{{Step: "one"}, {Step: "two"}}
		_, err := runner.Run(ctx, work)
		require.ErrorContains(t, err, "concurrent peer delivery")
		list, err := manager.List(work.ExecutionID)
		require.NoError(t, err)
		require.Empty(t, list)
	})
}
