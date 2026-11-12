package comms_test

import (
	"context"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/testdb"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSharedAssignments(t *testing.T) {
	db := testdb.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	stop, err := service.Listen(ctx)
	require.NoError(t, err)
	defer stop()
	scope, err := service.CreateScope(ctx, "assignment-test")
	require.NoError(t, err)
	owner, err := service.RegisterPeer(ctx, scope.ID, "operator", "operator")
	require.NoError(t, err)
	worker, err := service.RegisterPeer(ctx, scope.ID, "worker", "worker")
	require.NoError(t, err)
	other, err := service.RegisterPeer(ctx, scope.ID, "other", "other")
	require.NoError(t, err)
	principal, err := service.Authenticate(ctx, owner.Token)
	require.NoError(t, err)
	thread, err := service.Create(ctx, principal, comms.Create{OperationID: "thread", Title: "assignment", Participants: []string{worker.ParticipantID, other.ParticipantID}})
	require.NoError(t, err)
	assignment := comms.Assignment{Version: 1, ID: uuid.NewString(), Owner: worker.ParticipantID, ThreadID: thread.ThreadID, Recipient: owner.ParticipantID, Mode: "fabric", Input: map[string]any{"task": "hello"}, Configuration: map[string]any{"model": "test"}, OutputSchema: json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)}
	require.NoError(t, service.PutAssignment(ctx, assignment))
	require.NoError(t, service.PutAssignment(ctx, assignment))
	changed := assignment
	changed.Input = "changed"
	require.Error(t, service.PutAssignment(ctx, changed))
	client := connect(t, ctx, worker)
	peer := connect(t, ctx, other)
	fetched := call(t, ctx, client, "assignment_get", map[string]any{})
	require.Contains(t, string(fetched), `"task":"hello"`)
	denied, err := peer.CallTool(ctx, toolParams("assignment_get", map[string]any{"id": assignment.ID}))
	require.NoError(t, err)
	require.True(t, denied.IsError)
	invalid, err := client.CallTool(ctx, toolParams("assignment_complete", map[string]any{"output": map[string]any{"answer": 42}}))
	require.NoError(t, err)
	require.True(t, invalid.IsError)
	call(t, ctx, client, "assignment_status", map[string]any{"state": "running"})
	result := map[string]any{"output": map[string]any{"answer": "yes"}}
	accepted := call(t, ctx, client, "assignment_complete", result)
	replayed := call(t, ctx, client, "assignment_complete", result)
	require.JSONEq(t, string(accepted), string(replayed))
	conflict, err := client.CallTool(ctx, toolParams("assignment_complete", map[string]any{"output": map[string]any{"answer": "no"}}))
	require.NoError(t, err)
	require.True(t, conflict.IsError)
	batch, err := service.Read(ctx, principal, comms.Read{ThreadID: thread.ThreadID})
	require.NoError(t, err)
	require.Len(t, batch.Messages, 1)
	require.Equal(t, "fabric.result", batch.Messages[0].Payload.Kind)
	// The same agent-facing operation retains existing Factory result validation/routing.
	work := execution.Work{ExecutionID: uuid.NewString(), Step: execution.Step{ID: "assess", Routes: map[execution.Outcome]execution.StepID{"completed": execution.Complete}}, Package: execution.PackageSnapshot{Name: "readiness"}, Visit: 1, Try: 1}
	factory, err := service.Provision(ctx, work)
	require.NoError(t, err)
	require.NoError(t, service.PutAssignment(ctx, comms.Assignment{Version: 1, ID: factory.ParticipantID, Owner: factory.ParticipantID, ThreadID: factory.ThreadID, Recipient: comms.ControllerID(work.ExecutionID), Mode: "factory", Input: map[string]any{"task": "hello"}, Configuration: map[string]any{}, OutputSchema: json.RawMessage(`{"type":"object"}`)}))
	factoryClient := connect(t, ctx, factory)
	call(t, ctx, factoryClient, "assignment_complete", map[string]any{"output": map[string]any{"outcome": "completed", "summary": "evaluated"}})
	persisted, err := service.Result(ctx, factory.ParticipantID)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	require.Equal(t, execution.Outcome("completed"), persisted.Outcome)
}

func toolParams(name string, args any) *mcp.CallToolParams {
	return &mcp.CallToolParams{Name: name, Arguments: args}
}
