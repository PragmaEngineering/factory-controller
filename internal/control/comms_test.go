package control

import (
	"context"
	"testing"

	"controls/execution"
	"controls/internal/comms"
	"controls/internal/testdb"

	"github.com/stretchr/testify/require"
)

func TestAgentInputUsesInboxWithoutApprovingWorkflow(t *testing.T) {
	db := testdb.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := Open(db)
	require.NoError(t, err)
	_, err = store.Create(NewExecution{ID: "input-test", Workflow: "fixture", Project: t.TempDir()})
	require.NoError(t, err)
	service, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	work := execution.Work{ExecutionID: "input-test", Step: execution.Step{ID: "worker", Routes: map[execution.Outcome]execution.StepID{"ready": execution.Complete}}, Package: execution.PackageSnapshot{Name: "worker"}, Visit: 1, Try: 1}
	connection, err := service.Provision(ctx, work)
	require.NoError(t, err)
	worker, err := service.Authenticate(ctx, connection.Token)
	require.NoError(t, err)
	sent, err := service.Send(ctx, worker, comms.Send{OperationID: "question", ThreadID: connection.ThreadID, Payload: comms.Structured("input_request", map[string]any{"prompt": "Which behavior?"})})
	require.NoError(t, err)
	engine := &recordingEngine{}
	controller := Controller{Store: store, Comms: service, Engine: engine}
	inbox, err := controller.Inbox("*")
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, "step_input", inbox[0].Kind)
	_, err = controller.Answer(ctx, sent.MessageID, "answer", "Use the smaller option", "human")
	require.NoError(t, err)
	require.Empty(t, engine.decisions, "an input response must not satisfy a configured workflow decision")
	inbox, err = controller.Inbox("*")
	require.NoError(t, err)
	require.Empty(t, inbox)
	require.NoError(t, service.FinishExecution(ctx, "input-test"))
	_, err = service.Send(ctx, worker, comms.Send{OperationID: "late", ThreadID: connection.ThreadID, Payload: comms.Structured("message", map[string]any{"text": "late"})})
	require.ErrorContains(t, err, "attempt_inactive")
}
