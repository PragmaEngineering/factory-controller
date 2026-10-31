package control

import (
	"context"
	"testing"

	"controls/execution"
	"controls/internal/testdb"

	"github.com/stretchr/testify/require"
)

type hookRetryEngine struct {
	recordingEngine
	retries []execution.Retry
}

func (e *hookRetryEngine) SendRetry(_ context.Context, retry execution.Retry) error {
	e.retries = append(e.retries, retry)
	return nil
}

func TestHookRetryUsesItsOwnIdentityAndPreservesHostDiagnostics(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	require.NoError(t, err)
	_, err = store.Create(NewExecution{ID: "hook-run", Workflow: "test", Project: t.TempDir()})
	require.NoError(t, err)
	scope := execution.HookContext{Version: 1, ExecutionID: "hook-run", Scope: "step", Boundary: "before", Name: "prepare", Step: "build", StepVisit: 1, StepAttempt: 1}
	scope.InvocationID = scope.ID()
	pending := execution.HookRun{ID: scope.InvocationID, Context: scope, Try: 1, State: "pending"}
	require.NoError(t, store.HookProgress(execution.Progress{Kind: "hook_pending", ExecutionID: scope.ExecutionID, Hook: &pending}))
	work := execution.HookWork{Context: scope, Try: 1, Hook: execution.Hook{Name: "prepare", Command: "sh"}}
	run, claimed, err := store.ClaimHook(work)
	require.NoError(t, err)
	require.True(t, claimed)
	run.SessionID = "hook-session"
	run.State = "failed"
	run.Error = "exit 7"
	require.NoError(t, store.SaveHook(scope.ExecutionID, run))
	blocked := pending
	blocked.State = "blocked"
	blocked.Error = "exit 7"
	require.NoError(t, store.HookProgress(execution.Progress{Kind: "hook_blocked", ExecutionID: scope.ExecutionID, Hook: &blocked}))
	record, err := store.Get(scope.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, "hook-session", record.Hooks[0].SessionID)
	engine := &hookRetryEngine{}
	controller := Controller{Store: store, Engine: engine}
	record, err = controller.Retry(context.Background(), run.ID, "operator", "fixed environment")
	require.NoError(t, err)
	require.Equal(t, execution.ExecutionRunning, record.State)
	require.Len(t, engine.retries, 1)
	require.Equal(t, execution.StepID(run.ID), engine.retries[0].Step)
	require.EqualValues(t, 1, engine.retries[0].Visit)
	work.Try = 2
	run, claimed, err = store.ClaimHook(work)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, scope.InvocationID, run.ID)
	run.State = "succeeded"
	run.SessionID = "second-session"
	require.NoError(t, store.SaveHook(scope.ExecutionID, run))
	record, err = store.Get(scope.ExecutionID)
	require.NoError(t, err)
	require.Equal(t, execution.StepVisitID(scope.ExecutionID, "build", 1), record.CurrentStepID)
	_, claimed, err = store.ClaimHook(work)
	require.NoError(t, err)
	require.False(t, claimed)
	_, err = controller.Retry(context.Background(), run.ID, "operator", "duplicate")
	require.Error(t, err)
}
