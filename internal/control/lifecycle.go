package control

import (
	"fmt"
	"time"

	"controls/execution"
)

// Attempt and StageVisit are controller-observed lifecycle records, persisted
// atomically with progress events. Replayed events never reset their timestamps.
type Attempt struct {
	Try        uint                  `json:"try"`
	State      string                `json:"state"`
	StartedAt  time.Time             `json:"started_at"`
	FinishedAt *time.Time            `json:"finished_at,omitempty"`
	Result     *execution.StepResult `json:"result,omitempty"`
	Error      string                `json:"error,omitempty"`
}
type StageVisit struct {
	Name       string     `json:"name"`
	Visit      uint       `json:"visit"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func recordStage(record *Execution, p execution.Progress, at time.Time) (bool, error) {
	if p.Stage == "" || p.StageVisit == 0 {
		return false, fmt.Errorf("%s needs stage name and visit", p.Kind)
	}
	for i := range record.Stages {
		stage := &record.Stages[i]
		if stage.Name != p.Stage || stage.Visit != p.StageVisit {
			continue
		}
		if p.Kind == "stage_started" || stage.FinishedAt != nil {
			return false, nil
		}
		stage.State = "completed"
		stage.FinishedAt = &at
		return true, nil
	}
	if p.Kind != "stage_started" {
		return false, fmt.Errorf("stage %s visit %d was not started", p.Stage, p.StageVisit)
	}
	record.Stages = append(record.Stages, StageVisit{Name: p.Stage, Visit: p.StageVisit, State: "running", StartedAt: at})
	return true, nil
}

func recordAttempt(step *Step, p execution.Progress, at time.Time) (bool, bool, error) {
	if p.Kind != "attempt_started" && p.Kind != "attempt_completed" && p.Kind != "attempt_failed" {
		return false, false, nil
	}
	if p.Try == 0 {
		return false, true, fmt.Errorf("attempt number must be positive")
	}
	var attempt *Attempt
	for i := range step.Attempts {
		if step.Attempts[i].Try == p.Try {
			attempt = &step.Attempts[i]
			break
		}
	}
	if p.Kind == "attempt_started" {
		if attempt != nil || (step.State == "completed" && step.Try >= p.Try) {
			return false, true, nil
		}
		step.Attempts = append(step.Attempts, Attempt{Try: p.Try, State: "running", StartedAt: at})
		step.Try = p.Try
		step.State = "running"
		step.Error = ""
		return true, true, nil
	}
	if attempt == nil {
		if step.State == "completed" && step.Try >= p.Try {
			return false, true, nil
		}
		return false, true, fmt.Errorf("attempt %d was not started", p.Try)
	}
	if attempt.FinishedAt != nil {
		return false, true, nil
	}
	if p.Kind == "attempt_completed" && p.Result == nil {
		return false, true, fmt.Errorf("completed attempt needs result")
	}
	attempt.FinishedAt = &at
	attempt.Result = p.Result
	attempt.Error = p.Error
	if p.Kind == "attempt_failed" {
		attempt.State = "failed"
		step.Error = p.Error
	} else {
		attempt.State = "completed"
		step.Outcome = p.Result.Outcome
	}
	step.Try = p.Try
	return true, true, nil
}

func finishLifecycle(record *Execution, state string, at time.Time) {
	if record.FinishedAt == nil {
		record.FinishedAt = &at
	}
	for i := range record.Stages {
		stage := &record.Stages[i]
		if stage.FinishedAt == nil {
			stage.State = state
			stage.FinishedAt = &at
		}
	}
	for i := range record.Steps {
		for j := range record.Steps[i].Attempts {
			attempt := &record.Steps[i].Attempts[j]
			if attempt.FinishedAt == nil {
				attempt.State = state
				attempt.FinishedAt = &at
			}
		}
	}
}
