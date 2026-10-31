package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controls/execution"
	"controls/internal/reports"
	"controls/internal/sessions"
	"controls/internal/telemetry"
	"controls/internal/testdb"

	"github.com/stretchr/testify/require"
)

func TestPersistedLifecycleReportAndClient(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	store, err := Open(db)
	require.NoError(t, err)
	_, err = store.Create(NewExecution{ID: "report-run", Workflow: "example", ConfigDigest: "revision-1"})
	require.NoError(t, err)
	step := execution.Step{ID: "build", Stage: "delivery", Kind: execution.RunStep}
	result := execution.StepResult{Outcome: "completed", Summary: "Agent claims completion", Actor: "worker", At: time.Now().UTC()}
	observer := ProgressObserver{Store: store}
	progresses := []execution.Progress{
		{Kind: "stage_started", Stage: "delivery", StageVisit: 1},
		{Kind: "step_started", Step: step, Visit: 1},
		{Kind: "attempt_started", Step: step, Visit: 1, Try: 1},
		{Kind: "attempt_failed", Step: step, Visit: 1, Try: 1, Error: "temporary exit"},
		{Kind: "attempt_started", Step: step, Visit: 1, Try: 2},
		{Kind: "attempt_completed", Step: step, Visit: 1, Try: 2, Result: &result},
		{Kind: "step_completed", Step: step, Visit: 1, Try: 2, Result: &result},
		{Kind: "stage_completed", Stage: "delivery", StageVisit: 1},
		{Kind: "execution_completed"},
	}
	for _, p := range progresses {
		p.ExecutionID = "report-run"
		require.NoError(t, observer.Record(ctx, p))
	}
	before, err := store.Get("report-run")
	require.NoError(t, err)
	for _, p := range progresses {
		p.ExecutionID = "report-run"
		require.NoError(t, observer.Record(ctx, p))
	}
	after, err := store.Get("report-run")
	require.NoError(t, err)
	require.Equal(t, before, after, "replay must retain first observed lifecycle times and attempt results")
	require.Len(t, after.Steps[0].Attempts, 2)
	require.Equal(t, "failed", after.Steps[0].Attempts[0].State)
	require.NotNil(t, after.FinishedAt)
	require.Equal(t, result.Summary, after.Steps[0].Result.Summary)
	sessionID := "report-session"
	require.NoError(t, db.Create(ctx, "session", sessionID, sessions.Session{ID: sessionID, ExecutionID: "report-run", StepID: after.Steps[0].ID, Name: "builder", Runner: "any-runner"}))
	require.NoError(t, db.Create(ctx, "telemetry", sessionID, telemetry.Projection{Summary: telemetry.Summary{Samples: 2, Tokens: telemetry.Tokens{Input: 10, CacheRead: 20, Output: 5}, Provider: "custom", Model: "custom-model"}}))
	handler := Handler(&Controller{Store: store})
	// Use a new Store/HTTP client to prove reports need no runner/process state.
	reopened, err := Open(db)
	require.NoError(t, err)
	document, err := reopened.Report(ctx, reports.Query{ExecutionID: "report-r"})
	require.NoError(t, err)
	require.EqualValues(t, 35, document.Snapshot.Selected.Resources.Tokens.Total())
	require.Len(t, document.Snapshot.Selected.Stages, 1)
	require.Len(t, document.Snapshot.Selected.Assessments, 1)
	require.Equal(t, "custom-model", document.Snapshot.Selected.Agents[0].Model)
	require.NotContains(t, document.HTML, "ZgotmplZ")
	// Later accounting must not mutate a previously saved report snapshot.
	require.NoError(t, db.Change(ctx, "telemetry", sessionID, func([]byte) ([]byte, error) {
		return json.Marshal(telemetry.Projection{Summary: telemetry.Summary{Samples: 5, Tokens: telemetry.Tokens{Input: 1000}}})
	}))
	client := NewClientWithHTTPClient(&http.Client{Transport: reportHandlerTransport{handler}})
	saved, err := client.Report(ctx, reports.Query{ReportID: document.ID})
	require.NoError(t, err)
	require.Equal(t, document, saved)
	_, err = reopened.Report(ctx, reports.Query{ExecutionID: "report-run", Factory: "other"})
	require.ErrorContains(t, err, "does not belong")
	cutoff := time.Now().UTC().Add(-time.Hour)
	old, err := reopened.Report(ctx, reports.Query{ExecutionID: "report-run", To: &cutoff})
	require.NoError(t, err)
	require.Empty(t, old.Snapshot.History)
	require.NotNil(t, old.Snapshot.Selected)
}

type reportHandlerTransport struct{ handler http.Handler }

func (t reportHandlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, r)
	return recorder.Result(), nil
}
