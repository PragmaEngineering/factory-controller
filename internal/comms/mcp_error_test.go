package comms

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"controls/execution"
	"github.com/stretchr/testify/require"
)

func TestMCPFailureDoesNotTreatUnknownErrorsAsTransient(t *testing.T) {
	result := mcpFailure(errors.New("private database diagnostic"))
	require.True(t, result.IsError)
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var body struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Equal(t, "internal_error", body.Error.Code)
	require.Contains(t, body.Error.Message, "inspect controller diagnostics")
	require.NotContains(t, string(raw), "private database diagnostic")
	require.NotContains(t, string(raw), "retry unchanged")
}

func TestMCPFailurePreservesExplicitTransientClassification(t *testing.T) {
	failure := fault("temporarily_unavailable", "participant rate limit reached; retry with backoff")
	result := mcpFailure(fmt.Errorf("wrapped: %w", failure))
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"error":{"code":"temporarily_unavailable","message":"participant rate limit reached; retry with backoff"}}`, string(raw))
}

func TestMalformedControlDataDoesNotExposeSubmittedValues(t *testing.T) {
	_, err := decodeData[execution.StepResult](Structured("step_result", map[string]any{
		"outcome": "completed", "summary": "done", "at": "private-invalid-timestamp",
	}))
	var failure *Fault
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "invalid_argument", failure.Code)
	require.NotContains(t, failure.Message, "private-invalid-timestamp")
	require.Contains(t, failure.Message, "correct the payload")
}
