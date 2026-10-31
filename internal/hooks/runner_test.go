package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"controls/execution"
	"controls/internal/control"
	"controls/internal/hooks"
	"controls/internal/sessions"
	"controls/internal/testdb"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSupervisedHookPersistsJSONInputOutputAndSkipsSuccessfulReplay(t *testing.T) {
	db := testdb.Open(t)
	store, err := control.Open(db)
	require.NoError(t, err)
	root := t.TempDir()
	project := t.TempDir()
	manager, err := sessions.Open(root, "", db)
	require.NoError(t, err)
	record, err := store.Create(control.NewExecution{ID: uuid.NewString(), Workflow: "test", Project: project})
	require.NoError(t, err)
	scope := execution.HookContext{Version: 1, ExecutionID: record.ID, Project: project, Scope: "execution", Boundary: "before", Name: "prepare"}
	scope.InvocationID = scope.ID()
	script := filepath.Join(project, "utility.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ncat > input.json\nprintf '%s' \"$1\" > argument.txt\nprintf 'called\\n' >> calls.txt\nprintf 'hook output\\n'\n"), 0700))
	work := execution.HookWork{Context: scope, Try: 1, Hook: execution.Hook{Name: "prepare", Command: "sh", Arguments: []string{script, "literal argument with spaces"}}}
	runner := hooks.Runner{Recorder: store, Sessions: manager}
	run, err := runner.RunHook(context.Background(), work)
	require.NoError(t, err)
	require.Equal(t, "succeeded", run.State)
	input, err := os.ReadFile(filepath.Join(project, "input.json"))
	require.NoError(t, err)
	require.Contains(t, string(input), scope.InvocationID)
	argument, err := os.ReadFile(filepath.Join(project, "argument.txt"))
	require.NoError(t, err)
	require.Equal(t, "literal argument with spaces", string(argument))
	output, err := manager.Output(run.SessionID, 0, 65536)
	require.NoError(t, err)
	require.Contains(t, output.Text, "hook output")
	require.NotContains(t, output.Text, scope.InvocationID, "stdin must not be echoed into the output PTY")
	_, err = manager.Send(run.SessionID, "unexpected interactive message")
	require.Error(t, err)
	// Reopen persisted records and replay the same hook: no second process.
	store, err = control.Open(db)
	require.NoError(t, err)
	runner.Recorder = store
	repeated, err := runner.RunHook(context.Background(), work)
	require.NoError(t, err)
	require.Equal(t, run.SessionID, repeated.SessionID)
	calls, err := os.ReadFile(filepath.Join(project, "calls.txt"))
	require.NoError(t, err)
	require.Equal(t, "called\n", string(calls))
	resource, err := (&control.Controller{Store: store, Sessions: manager}).Resource(run.ID)
	require.NoError(t, err)
	require.Equal(t, "hook", resource.Type)
}

func TestFailedHookRetryRetainsInvocationAndDoesNotReuseFailedProcess(t *testing.T) {
	db := testdb.Open(t)
	store, err := control.Open(db)
	require.NoError(t, err)
	project := t.TempDir()
	manager, err := sessions.Open(t.TempDir(), "", db)
	require.NoError(t, err)
	record, err := store.Create(control.NewExecution{ID: uuid.NewString(), Workflow: "test", Project: project})
	require.NoError(t, err)
	scope := execution.HookContext{Version: 1, ExecutionID: record.ID, Project: project, Scope: "stage", Boundary: "after", Stage: "work", StageVisit: 1, Name: "commit"}
	scope.InvocationID = scope.ID()
	work := execution.HookWork{Context: scope, Try: 1, Hook: execution.Hook{Name: "commit", Command: "sh", Arguments: []string{"-c", "cat >> contexts.json; if [ ! -f attempted ]; then touch attempted; printf 'failure detail\\n'; exit 7; fi"}}}
	runner := hooks.Runner{Recorder: store, Sessions: manager}
	failed, err := runner.RunHook(context.Background(), work)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.State)
	require.Contains(t, failed.Error, "exited 7")
	work.Try = 2
	success, err := runner.RunHook(context.Background(), work)
	require.NoError(t, err)
	require.Equal(t, "succeeded", success.State)
	require.Equal(t, failed.ID, success.ID)
	require.NotEqual(t, failed.SessionID, success.SessionID)
	all, err := manager.List(record.ID)
	require.NoError(t, err)
	require.Len(t, all, 2)
}
