package control

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"controls/execution"
	"controls/internal/sessions"
	"controls/internal/testdb"

	"github.com/stretchr/testify/require"
)

type recordingSessionStopper struct {
	executionID string
	stopped     []string
	sessions    []sessions.Session
}

type recordingEngine struct {
	decisions []execution.Decision
}

func (*recordingEngine) Start(context.Context, execution.StartRequest) (execution.Started, error) {
	return execution.Started{}, nil
}
func (e *recordingEngine) SendDecision(_ context.Context, decision execution.Decision) error {
	e.decisions = append(e.decisions, decision)
	return nil
}
func (*recordingEngine) Cancel(context.Context, string) error             { return nil }
func (*recordingEngine) SendRetry(context.Context, execution.Retry) error { return nil }

func TestAnswerRejectsFailedExecution(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewExecution{ID: "run", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	step := execution.Step{ID: "approval", Title: "Approval", Decision: &execution.DecisionSpec{
		Authority: "human", Options: []execution.Outcome{"approve"}, Prompt: "Approve?",
	}}
	observer := ProgressObserver{Store: store}
	for _, progress := range []execution.Progress{
		{Kind: "step_started", ExecutionID: "run", Step: step, Visit: 1},
		{Kind: "decision_requested", ExecutionID: "run", Step: step, Visit: 1},
		{Kind: "execution_failed", ExecutionID: "run", Error: "durable replay failed"},
	} {
		if err := observer.Record(context.Background(), progress); err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.Get("run")
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingEngine{}
	controller := Controller{Store: store, Engine: engine}
	if _, err := controller.Answer(context.Background(), record.Requests[0].ID, "approve", "approved", "human"); err == nil {
		t.Fatal("approval was accepted after the execution failed")
	}
	if len(engine.decisions) != 0 {
		t.Fatal("decision was sent to a failed durable execution")
	}
	record, err = store.Get("run")
	if err != nil || record.State != execution.ExecutionFailed {
		t.Fatalf("failed execution state changed after rejected approval: %+v, %v", record, err)
	}
}

func TestAnswerPublishesStableDecisionTimestamp(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewExecution{ID: "run", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	step := execution.Step{ID: "approval", Title: "Approval", Decision: &execution.DecisionSpec{
		Authority: "human", Options: []execution.Outcome{"approve"}, Prompt: "Approve?",
	}}
	observer := ProgressObserver{Store: store}
	for _, kind := range []string{"step_started", "decision_requested"} {
		if err := observer.Record(context.Background(), execution.Progress{Kind: kind, ExecutionID: "run", Step: step, Visit: 1}); err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.Get("run")
	if err != nil {
		t.Fatal(err)
	}
	engine := &recordingEngine{}
	controller := Controller{Store: store, Engine: engine}
	record, err = controller.Answer(context.Background(), record.Requests[0].ID, "approve", "approved", "human")
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.decisions) != 1 || engine.decisions[0].At.IsZero() || !engine.decisions[0].At.Equal(*record.Requests[0].AnsweredAt) {
		t.Fatal("the event and stored approval have different timestamps")
	}
}

func TestStageLimitIsVisibleInHumanDecision(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewExecution{ID: "run", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	step := execution.Step{ID: "approval", Stage: "design", Kind: execution.DecisionStep,
		Decision: &execution.DecisionSpec{Authority: "human", Prompt: "Review the design", Options: []execution.Outcome{"approve"}}}
	observer := ProgressObserver{Store: store}
	for _, progress := range []execution.Progress{
		{Kind: "step_started", ExecutionID: "run", Step: step, Visit: 1},
		{Kind: "decision_requested", ExecutionID: "run", Step: step, Visit: 1, Message: "Stage design reached its 3-round limit with unresolved findings."},
	} {
		if err := observer.Record(context.Background(), progress); err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.Get("run")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Requests) != 1 || record.Requests[0].Prompt != "Stage design reached its 3-round limit with unresolved findings. Review the design" {
		t.Fatalf("human decision did not explain the escalation: %+v", record.Requests)
	}
}

func TestStageLimitEventIsStableAcrossReplay(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewExecution{ID: "run", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	step := execution.Step{ID: "review", Stage: "design"}
	observer := ProgressObserver{Store: store}
	started := execution.Progress{Kind: "step_started", ExecutionID: "run", Step: step, Visit: 3}
	limit := execution.Progress{Kind: "stage_limit_reached", ExecutionID: "run", Step: step, Visit: 3, Message: "Review limit reached"}
	for _, progress := range []execution.Progress{started, limit, started, limit} {
		if err := observer.Record(context.Background(), progress); err != nil {
			t.Fatal(err)
		}
	}
	record, err := store.Get("run")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range record.Events {
		if event.Kind == "stage_limit_reached" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replay recorded %d stage-limit events", count)
	}
}

func TestCompletedStepIsUnchangedByDurableReplay(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewExecution{ID: "run", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	step := execution.Step{ID: "build", Title: "Build"}
	result := execution.StepResult{Outcome: "ready", Summary: "done", Actor: "builder"}
	sequence := []execution.Progress{
		{Kind: "step_started", ExecutionID: "run", Step: step, Visit: 1},
		{Kind: "attempt_started", ExecutionID: "run", Step: step, Visit: 1, Try: 1},
		{Kind: "attempt_completed", ExecutionID: "run", Step: step, Visit: 1, Try: 1, Result: &result},
		{Kind: "step_completed", ExecutionID: "run", Step: step, Visit: 1, Try: 1, Result: &result},
	}
	observer := ProgressObserver{Store: store}
	for _, progress := range sequence {
		if err := observer.Record(context.Background(), progress); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Get("run")
	if err != nil {
		t.Fatal(err)
	}
	for _, progress := range sequence {
		if err := observer.Record(context.Background(), progress); err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Get("run")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Events) != len(before.Events) || after.Steps[0].State != "completed" {
		t.Fatalf("replay changed a completed step: before=%+v after=%+v", before.Steps[0], after.Steps[0])
	}
}

func (s *recordingSessionStopper) List(executionID string) ([]sessions.Session, error) {
	s.executionID = executionID
	return s.sessions, nil
}

func (s *recordingSessionStopper) Stop(id string) error {
	s.stopped = append(s.stopped, id)
	return nil
}

func TestFailedExecutionStopsRunningSessions(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(NewExecution{ID: "failed-run", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	stopper := &recordingSessionStopper{sessions: []sessions.Session{
		{ID: "active", State: sessions.Running},
		{ID: "finished", State: sessions.Exited},
	}}
	observer := ProgressObserver{Store: store, Sessions: stopper}
	failure := execution.Progress{Kind: "execution_failed", ExecutionID: "failed-run", Error: "agent timed out"}
	if err := observer.Record(context.Background(), failure); err != nil {
		t.Fatal(err)
	}
	if stopper.executionID != "failed-run" || len(stopper.stopped) != 1 || stopper.stopped[0] != "active" {
		t.Fatalf("failed execution did not stop only its running session: %+v", stopper)
	}
	record, err := store.Get("failed-run")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != execution.ExecutionFailed || record.Error != "agent timed out" {
		t.Fatalf("failure was not persisted: %+v", record)
	}
}

func TestContextsAndProgressSurviveReopen(t *testing.T) {
	root := t.TempDir()
	db := testdb.Open(t)
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create(NewExecution{ID: "first", Description: "small change", Workflow: "go", Project: root})
	if err != nil {
		t.Fatal(err)
	}
	if first.ContextID == "" || first.ContextID == "*" {
		t.Fatalf("unexpected context ID %q", first.ContextID)
	}
	if err := store.SetContext(first.ContextID); err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(NewExecution{ID: "second", Workflow: "go", Project: root})
	if err != nil {
		t.Fatal(err)
	}
	if second.ContextID != first.ContextID {
		t.Fatalf("selected context not reused: %q vs %q", second.ContextID, first.ContextID)
	}
	step := execution.Step{ID: "build", Title: "Build"}
	observer := ProgressObserver{Store: store}
	started := execution.Progress{Kind: "step_started", ExecutionID: first.ID, Step: step, Visit: 1}
	if err := observer.Record(context.Background(), started); err != nil {
		t.Fatal(err)
	}
	if err := observer.Record(context.Background(), started); err != nil {
		t.Fatal(err)
	}
	blocked := execution.Progress{Kind: "step_blocked", ExecutionID: first.ID, Step: step, Visit: 1, Try: 1, Error: "process exited"}
	if err := observer.Record(context.Background(), blocked); err != nil {
		t.Fatal(err)
	}
	if err := observer.Record(context.Background(), blocked); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	record, err := reopened.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != execution.ExecutionBlocked || len(record.Steps) != 1 || record.Steps[0].Error != "process exited" {
		t.Fatalf("unexpected persisted progress: %+v", record)
	}
	if got := len(record.Events); got != 3 { // created, started, blocked; replay added nothing
		t.Fatalf("expected 3 events, got %d", got)
	}
	if selected, _, err := reopened.GetContext(); err != nil || selected != first.ContextID {
		t.Fatalf("selected context lost: %q", selected)
	}
	if list, err := reopened.List(""); err != nil || len(list) != 2 {
		t.Fatalf("selected-context list: %v, %d", err, len(list))
	}
}

func TestConcurrentManagerReplicasPreserveEveryEvent(t *testing.T) {
	db := testdb.Open(t)
	first, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Create(NewExecution{ID: "shared", Workflow: "go", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	const updates = 24
	var group sync.WaitGroup
	errors := make(chan error, updates)
	for i := range updates {
		group.Add(1)
		go func() {
			defer group.Done()
			store := first
			if i%2 == 1 {
				store = second
			}
			_, err := store.Update("shared", "note", fmt.Sprintf("note %d", i), "", func(_ *Execution) error { return nil })
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	record, err := second.Get("shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Events) != updates+1 {
		t.Fatalf("lost an event: got %d, want %d", len(record.Events), updates+1)
	}
}

// Engine cancellation can synchronously emit shutdown progress. The persisted
// cancellation fence must already exist when those callbacks arrive.
type cancellationEngine struct {
	recordingEngine
	onCancel func() error
	onRetry  func() error
}

func (e *cancellationEngine) Cancel(context.Context, string) error             { return e.onCancel() }
func (e *cancellationEngine) SendRetry(context.Context, execution.Retry) error { return e.onRetry() }

func TestCancellationFencesAllLateProgress(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	require.NoError(t, err)
	_, err = store.Create(NewExecution{ID: "cancel-run", Workflow: "go", Project: t.TempDir()})
	require.NoError(t, err)
	_, err = store.Update("cancel-run", "submitted", "submitted", "", func(e *Execution) error { e.WorkflowRunID = "engine-run"; return nil })
	require.NoError(t, err)
	observer := ProgressObserver{Store: store}
	step := execution.Step{ID: "author", Stage: "design", Decision: &execution.DecisionSpec{Authority: "human", Options: []execution.Outcome{"approve"}}}
	for _, kind := range []string{"step_started", "decision_requested"} {
		require.NoError(t, observer.Record(context.Background(), execution.Progress{Kind: kind, ExecutionID: "cancel-run", Step: step, Visit: 1}))
	}
	engine := &cancellationEngine{onCancel: func() error {
		current, err := store.Get("cancel-run")
		if err != nil {
			return err
		}
		if current.State != execution.ExecutionCancelled {
			return fmt.Errorf("engine cancelled before durable fence: %s", current.State)
		}
		for _, kind := range []string{"stage_round_interrupted", "attempt_failed", "step_blocked", "step_completed", "attempt_started", "decision_requested", "step_started", "execution_failed", "execution_completed"} {
			if err := observer.Record(context.Background(), execution.Progress{Kind: kind, ExecutionID: "cancel-run", Step: step, Visit: 1, Try: 1, Error: "context canceled"}); err != nil {
				return err
			}
		}
		return nil
	}}
	controller := Controller{Store: store, Engine: engine}
	for range 2 {
		_, err := controller.Cancel(context.Background(), "cancel-run")
		require.NoError(t, err)
	}
	current, err := store.Get("cancel-run")
	require.NoError(t, err)
	require.Equal(t, execution.ExecutionCancelled, current.State)
	require.Equal(t, "cancelled", current.Steps[0].State)
	require.Empty(t, current.CurrentStepID)
	require.Len(t, current.Requests, 1)
	require.Equal(t, "cancelled", current.Requests[0].State)
	count := 0
	for _, event := range current.Events {
		require.NotContains(t, []string{"execution_failed", "step_blocked"}, event.Kind)
		if event.Kind == "execution_cancelled" {
			count++
		}
	}
	require.Equal(t, 1, count, "repeated cancellation must not append another event")
}

func TestRetryDeliveryFailureDoesNotUndoCancellation(t *testing.T) {
	store, err := Open(testdb.Open(t))
	require.NoError(t, err)
	_, err = store.Create(NewExecution{ID: "retry-cancel-run", Workflow: "go", Project: t.TempDir()})
	require.NoError(t, err)
	step := execution.Step{ID: "work"}
	observer := ProgressObserver{Store: store}
	for _, kind := range []string{"step_started", "step_blocked"} {
		require.NoError(t, observer.Record(context.Background(), execution.Progress{Kind: kind, ExecutionID: "retry-cancel-run", Step: step, Visit: 1, Try: 1}))
	}
	engine := &cancellationEngine{onCancel: func() error { return nil }}
	controller := Controller{Store: store, Engine: engine}
	engine.onRetry = func() error {
		_, err := controller.Cancel(context.Background(), "retry-cancel-run")
		if err != nil {
			return err
		}
		return fmt.Errorf("retry transport interrupted")
	}
	_, err = controller.Retry(context.Background(), StepID("retry-cancel-run", "work", 1), "human", "retry")
	require.ErrorContains(t, err, "retry transport interrupted")
	current, err := store.Get("retry-cancel-run")
	require.NoError(t, err)
	require.Equal(t, execution.ExecutionCancelled, current.State)
	require.Equal(t, "cancelled", current.Steps[0].State)
}
