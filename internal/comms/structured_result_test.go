package comms_test

import (
	"context"
	"encoding/json"
	"testing"

	"controls/execution"
	"controls/internal/comms"
	"controls/internal/testdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNamedResultOutputsSurviveMCPAcceptanceAndServiceReopen(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	service, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	work := execution.Work{ExecutionID: uuid.NewString(), Step: execution.Step{ID: "evaluate", Routes: map[execution.Outcome]execution.StepID{"completed": "architect"}}, Package: execution.PackageSnapshot{Name: "readiness"}, Visit: 1, Try: 1}
	connection, err := service.Provision(ctx, work)
	require.NoError(t, err)
	participant, err := service.Authenticate(ctx, connection.Token)
	require.NoError(t, err)
	participants, err := service.Participants(ctx, participant, connection.ThreadID)
	require.NoError(t, err)
	controller := ""
	for _, p := range participants {
		if p.Kind == "controller" {
			controller = p.ID
		}
	}
	require.NotEmpty(t, controller)
	data := map[string]json.RawMessage{"request": json.RawMessage(`{"state":{"user_message":"Investigate slowness"},"questions":{"readiness":{"type":"choice"}}}`), "response": json.RawMessage(`{"answers":{"readiness":{"choice":"clarify"}}}`)}
	sent := comms.Send{OperationID: "result", ThreadID: connection.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("step_result", execution.StepResult{Outcome: "completed", Summary: "Evaluated", Data: data})}
	receipt, err := service.Send(ctx, participant, sent)
	require.NoError(t, err)
	require.Equal(t, "accepted", receipt.Effect.State)
	reopened, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	result, err := reopened.Result(ctx, participant.ID)
	require.NoError(t, err)
	require.NotNil(t, result)
	for name, raw := range data {
		require.JSONEq(t, string(raw), string(result.Data[name]))
	}
	next := execution.Work{Step: execution.Step{ID: "architect", InputBindings: map[string]execution.InputBinding{"evaluation_request": {Step: "evaluate", Output: "request"}, "evaluation_response": {Step: "evaluate", Output: "response"}}}, History: []execution.CompletedStep{{Step: "evaluate", Visit: 1, Try: 1, Result: *result}}}
	bound, err := execution.BindInputs(next)
	require.NoError(t, err)
	require.JSONEq(t, string(data["response"]), string(bound["evaluation_response"]))
	duplicate, err := service.Send(ctx, participant, sent)
	require.NoError(t, err)
	require.Equal(t, receipt.MessageID, duplicate.MessageID)
}
