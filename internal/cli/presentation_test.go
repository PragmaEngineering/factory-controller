package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"controls/internal/control"
	"github.com/stretchr/testify/require"
)

func TestPublicExecutionOmitsEngineDetails(t *testing.T) {
	record := control.Execution{
		WorkflowRunID: "private-run",
		Error:         "failed to wait for durable child workflow: context canceled",
		Events:        []control.Event{{Message: "Hatchet task failed"}},
	}
	var output bytes.Buffer
	if err := printJSON(&output, record); err != nil {
		t.Fatal(err)
	}
	visible := output.String()
	if strings.Contains(strings.ToLower(visible), "hatchet") || strings.Contains(visible, "private-run") {
		t.Fatalf("execution-engine details leaked: %s", visible)
	}
	if !strings.Contains(visible, "step execution was interrupted: context canceled") {
		t.Fatalf("failure details were lost: %s", visible)
	}
	if got := PublicError(errors.New("connect to self-hosted Hatchet: unavailable")).Error(); strings.Contains(strings.ToLower(got), "hatchet") || !strings.Contains(got, "unavailable") {
		t.Fatalf("public error = %q", got)
	}
}

func TestPublicErrorPreservesDiagnosticPathsAndCause(t *testing.T) {
	cause := errors.New("start Hatchet: database /Users/example/.hatchet-embedded/data is locked; see https://example.com/hatchet/logs")
	err := PublicError(cause)
	require.ErrorIs(t, err, cause)
	require.Contains(t, err.Error(), "start Factory execution service:")
	require.Contains(t, err.Error(), "/Users/example/.hatchet-embedded/data")
	require.Contains(t, err.Error(), "https://example.com/hatchet/logs")
}
