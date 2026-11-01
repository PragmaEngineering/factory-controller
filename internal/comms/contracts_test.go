package comms

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAdvertisedResultSchemaRejectsTheEvalPayload(t *testing.T) {
	contract := resolveContract(schemas["send_message"])
	args := map[string]any{"operation_id": "result-1", "thread_id": "thread", "payload": map[string]any{"kind": "step_result", "version": 1, "parts": []any{map[string]any{"kind": "data", "data": map[string]any{"outcome": "ready", "summary": "Done", "artifacts": []any{"reviews/build.md", "taskapi.go", "taskapi_test.go"}}}}}}
	require.Error(t, contract.Validate(args))
	data := args["payload"].(map[string]any)["parts"].([]any)[0].(map[string]any)["data"].(map[string]any)
	delete(data, "artifacts")
	require.NoError(t, contract.Validate(args))
	data["artifacts"] = []any{map[string]any{"name": "reviews/build.md"}}
	require.NoError(t, contract.Validate(args))
	delete(data, "summary")
	require.Error(t, contract.Validate(args))
	data["summary"] = "Done"
	data["interruption"] = map[string]any{"action": "stop"}
	require.Error(t, contract.Validate(args))
	// Custom A2A message kinds remain possible and cannot masquerade as step_result.
	args["payload"] = map[string]any{"kind": "custom-domain-event", "version": 1, "parts": []any{map[string]any{"kind": "data", "data": map[string]any{"custom": true}}}}
	require.NoError(t, contract.Validate(args))
}

func TestCompletionExampleIsAValidAdvertisedEnvelope(t *testing.T) {
	text := CompletionExample("assigned-thread", []string{"ready", "failed"})
	start := len("Completion contract — call send_message with this shape:\n```json\n")
	end := start
	for text[end:end+4] != "\n```" {
		end++
	}
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(text[start:end]), &args))
	require.NoError(t, resolveContract(schemas["send_message"]).Validate(args))
	require.Equal(t, "assigned-thread", args["thread_id"])
	require.Contains(t, text, "incoming-message delivery")
}
