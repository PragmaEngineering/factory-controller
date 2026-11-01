package comms

import (
	"encoding/json"
	"fmt"
	"sort"

	"controls/execution"
	"github.com/google/jsonschema-go/jsonschema"
)

// The same definitions are advertised to MCP clients and enforced by Send.
// Custom message kinds remain available, but cannot bypass reserved contracts.
var controlDataSchemas = map[string]map[string]any{
	"step_result": schema([]string{"outcome", "summary"}, map[string]any{
		"outcome": nonemptyString(), "summary": nonemptyString(),
		"publication_id": nonemptyString(),
		"artifacts":      map[string]any{"type": "array", "items": schema([]string{"name"}, map[string]any{"name": nonemptyString(), "uri": prop("string"), "digest": prop("string")})},
		"evidence":       map[string]any{"type": "array", "items": schema([]string{"kind", "uri"}, map[string]any{"kind": nonemptyString(), "uri": nonemptyString()})},
		"data":           prop("object"),
		// Existing deterministic clients serialize StepResult. These fields carry no authority.
		"actor": map[string]any{"type": "string", "description": "Ignored; authenticated identity is supplied by the host."},
		"at":    map[string]any{"type": "string", "description": "Ignored; timestamp is supplied by the host."},
	}),
	"publication":    schema([]string{"files"}, map[string]any{"files": map[string]any{"type": "array", "minItems": 1, "items": nonemptyString()}}),
	"input_request":  schema([]string{"prompt"}, map[string]any{"prompt": nonemptyString(), "options": map[string]any{"type": "array", "maxItems": 20, "items": prop("string")}}),
	"input_response": schema([]string{"request_id", "answer"}, map[string]any{"request_id": nonemptyString(), "answer": nonemptyString()}),
	"runtime_status": schema([]string{"state"}, map[string]any{"state": map[string]any{"type": "string", "enum": []string{"busy", "idle"}}}),
}

func nonemptyString() map[string]any { return map[string]any{"type": "string", "minLength": 1} }

func payloadSchema() map[string]any {
	branches := []any{}
	reserved := []string{}
	for _, kind := range []string{"step_result", "publication", "input_request", "input_response", "runtime_status"} {
		reserved = append(reserved, kind)
		dataPart := schema([]string{"kind", "data"}, map[string]any{"kind": map[string]any{"const": "data"}, "data": controlDataSchemas[kind]})
		textPart := schema([]string{"kind", "text"}, map[string]any{"kind": map[string]any{"const": "text"}, "text": prop("string"), "metadata": prop("object")})
		parts := map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"oneOf": []any{dataPart, textPart}}, "contains": dataPart, "minContains": 1, "maxContains": 1}
		branches = append(branches, schema([]string{"kind", "version", "parts"}, map[string]any{"kind": map[string]any{"const": kind}, "version": map[string]any{"type": "integer", "const": 1}, "parts": parts}))
	}
	branches = append(branches, schema([]string{"kind", "version", "parts"}, map[string]any{"kind": map[string]any{"type": "string", "minLength": 1, "not": map[string]any{"enum": reserved}}, "version": map[string]any{"type": "integer", "const": 1}, "parts": map[string]any{"type": "array", "minItems": 1, "items": prop("object")}}))
	return map[string]any{"oneOf": branches}
}

func resolveContract(value any) *jsonschema.Resolved {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var definition jsonschema.Schema
	if err = json.Unmarshal(raw, &definition); err != nil {
		panic(err)
	}
	resolved, err := definition.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return resolved
}

var resolvedPayloadContract = resolveContract(payloadSchema())
var resolvedSendContract = resolveContract(schemas["send_message"])

var resolvedControlData = func() map[string]*jsonschema.Resolved {
	out := map[string]*jsonschema.Resolved{}
	for kind, definition := range controlDataSchemas {
		out[kind] = resolveContract(definition)
	}
	return out
}()

func validateControlPayload(payload Payload) error {
	contract, known := resolvedControlData[payload.Kind]
	if !known {
		return nil
	}
	data, err := dataOf(payload)
	if err != nil {
		return err
	}
	// Keep field-specific decoder diagnostics for type mismatches, without exposing values.
	if payload.Kind == "step_result" {
		if _, err := decodeData[execution.StepResult](payload); err != nil {
			return err
		}
	}
	if err = contract.Validate(data); err != nil {
		// Validate individual known fields to identify the field without echoing submitted values.
		properties := controlDataSchemas[payload.Kind]["properties"].(map[string]any)
		keys := make([]string, 0, len(data))
		for key := range data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := data[key]
			field, ok := properties[key]
			if !ok {
				return fault("invalid_argument", "%s.data contains an unsupported field; use the advertised schema and correct the payload", payload.Kind)
			}
			if resolveContract(field).Validate(value) != nil {
				return fault("invalid_argument", "%s.data.%s does not match the advertised schema; correct the payload before resubmitting", payload.Kind, key)
			}
		}
		for _, key := range controlDataSchemas[payload.Kind]["required"].([]string) {
			if _, ok := data[key]; !ok {
				return fault("invalid_argument", "%s.data.%s is required; correct the payload before resubmitting", payload.Kind, key)
			}
		}
		return fault("invalid_argument", "invalid %s data; correct the payload using the advertised schema", payload.Kind)
	}
	raw, _ := json.Marshal(payload)
	var envelope any
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return fault("invalid_argument", "invalid payload envelope")
	}
	if err = resolvedPayloadContract.Validate(envelope); err != nil {
		return fault("invalid_argument", "payload.parts must contain exactly one typed data part and optional text parts matching the advertised schema")
	}
	return nil
}

// CompletionExample supplies a concrete envelope without choosing the worker's outcome.
func CompletionExample(thread string, outcomes []string) string {
	outcome := "CHOOSE_ONE_OF_THE_ALLOWED_OUTCOMES"
	if len(outcomes) == 1 {
		outcome = outcomes[0]
	}
	raw, _ := json.MarshalIndent(Send{OperationID: "UNIQUE_RESULT_OPERATION_ID", ThreadID: thread, Recipients: []string{"CONTROLLER_ID_FROM_LIST_PARTICIPANTS"}, Payload: Structured("step_result", map[string]any{"outcome": outcome, "summary": "REPLACE_WITH_A_FACTUAL_HANDOFF"})}, "", "  ")
	return fmt.Sprintf("Completion contract — call send_message with this shape:\n```json\n%s\n```\nReplace placeholder values. Allowed outcomes: %v. Normally omit artifacts; Controls collects configured outputs. Never supply filename strings in artifacts. If work was published, include its current publication_id in the data object. Wait for effect.state=accepted on this step_result call; acceptance of another message kind is not completion. Stored alone, an idle turn, or a process exit is not completion. A rejected outgoing call is not retried by automatic incoming-message delivery. Correct invalid_argument payloads and use a new operation_id; uncertain transport retries must preserve the original payload and operation_id. Do not repeat completed implementation or checks merely to repair this handoff.\n", raw, outcomes)
}
