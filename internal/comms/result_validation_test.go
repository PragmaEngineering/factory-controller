package comms_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"controls/execution"
	"controls/internal/artifacts"
	"controls/internal/comms"
	"controls/internal/testdb"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMalformedControlDataReturnsValidationErrorAndCanBeCorrected(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	service, err := comms.New(ctx, db, artifacts.HostCollector{Root: t.TempDir()})
	require.NoError(t, err)
	stop, err := service.Listen(ctx)
	require.NoError(t, err)
	t.Cleanup(stop)
	project := t.TempDir()
	for _, name := range []string{"spec.html", "plan.html"} {
		require.NoError(t, os.WriteFile(filepath.Join(project, name), []byte("<h1>Draft</h1>"), 0600))
	}
	work := execution.Work{
		ExecutionID: uuid.NewString(), Project: project,
		Step:    execution.Step{ID: "specification", Outputs: []string{"spec.html", "plan.html"}, Routes: map[execution.Outcome]execution.StepID{"completed": "review"}},
		Package: execution.PackageSnapshot{Name: "architect"}, Visit: 1, Try: 1,
	}
	connection, err := service.Provision(ctx, work)
	require.NoError(t, err)
	participant, err := service.Authenticate(ctx, connection.Token)
	require.NoError(t, err)
	members, err := service.Participants(ctx, participant, connection.ThreadID)
	require.NoError(t, err)
	var controller string
	for _, member := range members {
		if member.Kind == "controller" {
			controller = member.ID
		}
	}
	require.NotEmpty(t, controller)
	client := connect(t, ctx, connection)
	invalid := []struct {
		name, kind, field string
		data              map[string]any
	}{
		{"filename strings", "step_result", "artifacts", map[string]any{"outcome": "completed", "summary": "Drafted documents", "artifacts": []string{"spec.html", "plan.html"}}},
		{"wrong summary type", "step_result", "summary", map[string]any{"outcome": "completed", "summary": []string{"done"}}},
		{"wrong publication files", "publication", "files", map[string]any{"files": []int{1}}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "send_message", Arguments: comms.Send{
				OperationID: "invalid-" + tc.name, ThreadID: connection.ThreadID, Recipients: []string{controller}, Payload: comms.Structured(tc.kind, tc.data),
			}})
			require.NoError(t, err)
			require.True(t, result.IsError)
			raw, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			var body struct {
				Error struct{ Code, Message string }
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Equal(t, "invalid_argument", body.Error.Code)
			require.Contains(t, body.Error.Message, tc.field)
			require.NotContains(t, body.Error.Message, "retry unchanged")
			if tc.field == "artifacts" {
				require.Contains(t, body.Error.Message, `{"name":"output.txt"}`)
				require.Contains(t, body.Error.Message, "omit artifacts")
			}
			accepted, err := service.Result(ctx, participant.ID)
			require.NoError(t, err)
			require.Nil(t, accepted, "invalid requests must not complete the worker")
		})
	}
	// A rejected result produces a recorded controller correction, not an
	// unbounded model retry or a manufactured completion.
	require.NoError(t, service.Recover(ctx, participant.ID))
	batch, err := service.Read(ctx, participant, comms.Read{ThreadID: connection.ThreadID})
	require.NoError(t, err)
	require.Len(t, batch.Messages, 1)
	require.Equal(t, controller, batch.Messages[0].Sender)
	// Configured outputs are collected by the host; the agent need not describe
	// artifact objects or calculate file digests to finish its invocation.
	submission := comms.Send{OperationID: "corrected-result", ThreadID: connection.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("step_result", map[string]any{"outcome": "completed", "summary": "Drafted documents"})}
	raw := call(t, ctx, client, "send_message", submission)
	var receipt comms.Receipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	require.Equal(t, "accepted", receipt.Effect.State)
	result, err := service.Result(ctx, participant.ID)
	require.NoError(t, err)
	require.Len(t, result.Artifacts, 2)
	for _, artifact := range result.Artifacts {
		require.NotEmpty(t, artifact.URI)
		require.NotEmpty(t, artifact.Digest)
	}
	require.JSONEq(t, string(raw), string(call(t, ctx, client, "send_message", submission)), "successful retries reuse the receipt")
}
