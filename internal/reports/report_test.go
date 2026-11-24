package reports

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"controls/internal/telemetry"

	"github.com/stretchr/testify/require"
)

func TestWindowDefaultsToNinetyDaysAndValidates(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	from, to, err := (Query{}).Window(now)
	require.NoError(t, err)
	require.Equal(t, now, to)
	require.Equal(t, now.AddDate(0, 0, -90), from)
	later := now.Add(time.Second)
	_, _, err = (Query{To: &later}).Window(now)
	require.Error(t, err)
	_, _, err = (Query{From: &now, To: &now}).Window(now)
	require.Error(t, err)
}
func TestRenderIsReproducibleEscapesAssessmentAndPreservesUnknowns(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	end := at.Add(time.Minute)
	run := Execution{ID: "run", Factory: "golang", State: "completed", CreatedAt: at, FinishedAt: &end, Assessments: []Assessment{{Source: "agent result", Summary: "<script>alert(1)</script>"}}}
	snapshot := Snapshot{AsOf: end, From: at, To: end, Factory: "golang", Selected: &run, History: []Execution{run}}
	first, err := Build(snapshot)
	require.NoError(t, err)
	raw, err := json.Marshal(first.Snapshot)
	require.NoError(t, err)
	var recovered Snapshot
	require.NoError(t, json.Unmarshal(raw, &recovered))
	second, err := Build(recovered)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NotContains(t, first.HTML, "<script>")
	require.Contains(t, first.HTML, "&lt;script&gt;")
	require.Contains(t, first.HTML, "unknown")
	require.NotContains(t, first.HTML, "ZgotmplZ")
	require.Less(t, strings.Index(first.HTML, `id="measurements"`), strings.Index(first.HTML, `id="assessments"`))
	require.Nil(t, first.Snapshot.Statistics.MeanTokens)
	require.Equal(t, 0, first.Snapshot.Statistics.UsageN)
}
func TestStatisticsRetainFailuresAndMissingCoverage(t *testing.T) {
	at := time.Now().UTC()
	end := at.Add(20 * time.Second)
	history := []Execution{{State: "completed", CreatedAt: at, FinishedAt: &end, Resources: telemetry.Summary{Samples: 1, Tokens: telemetry.Tokens{Input: 100}}}, {State: "failed", CreatedAt: at, Resources: telemetry.Summary{Samples: 1, Tokens: telemetry.Tokens{Input: 20}}}, {State: "failed", CreatedAt: at}}
	stats := Summarize(history)
	require.Equal(t, 3, stats.Executions)
	require.Equal(t, 1, stats.Completed)
	require.Equal(t, 2, stats.UsageN)
	require.Equal(t, 60.0, *stats.MeanTokens)
	require.Equal(t, 1, stats.CompletedTimeN)
	require.Equal(t, 20.0, *stats.MeanCompletedSeconds)
}

func TestDetailedReportIncludesContextBoundariesAndEscapedFailureEvidence(t *testing.T) {
	at := time.Now().UTC()
	code := 2
	agent := Agent{Name: "builder", SessionID: "session", StepID: "step-visit", Diagnostics: telemetry.Diagnostics{
		Responses:  []telemetry.Response{{Number: 1, At: at, Tokens: telemetry.Tokens{Input: 100, Output: 50}, ContextWindow: 1000, StopReason: "toolUse"}},
		Boundaries: []telemetry.Boundary{{Kind: "turn_started", At: at}, {Kind: "turn_finished", At: at}},
		Failures:   []telemetry.Failure{{ToolID: "failed-call", Tool: telemetry.Tool{Name: "bash", Command: "go test", Arguments: `{"command":"go test"}`, StartedAt: at}, FinishedAt: at, Output: "<script>bad()</script>", ExitCode: &code, Truncated: true}},
	}}
	doc, err := Build(Snapshot{Selected: &Execution{Agents: []Agent{agent}}})
	require.NoError(t, err)
	for _, want := range []string{"15.0%", "step-visit", "failed-call", "turn_finished", "Evidence truncated", "&lt;script&gt;", "toolUse"} {
		require.Contains(t, doc.HTML, want)
	}
	require.NotContains(t, doc.HTML, "<script>")
	require.NotContains(t, doc.HTML, "ZgotmplZ")
}

func TestLegacyAgentSnapshotDoesNotAcquireEmptyDiagnosticFields(t *testing.T) {
	raw := `{"name":"legacy","session_id":"s","step_id":"step","runner":"command"}`
	var agent Agent
	require.NoError(t, json.Unmarshal([]byte(raw), &agent))
	encoded, err := json.Marshal(agent)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(encoded))
}
