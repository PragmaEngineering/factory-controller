package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controls/execution"
	"controls/internal/sessions"

	"github.com/google/uuid"
)

type sessionStopper interface {
	List(executionID string) ([]sessions.Session, error)
	Stop(sessionID string) error
}

type ProgressObserver struct {
	Comms interface {
		FinishExecution(context.Context, string) error
	}
	Store    *Store
	Sessions sessionStopper
}

func (o ProgressObserver) Record(_ context.Context, progress execution.Progress) error {
	if progress.Hook != nil {
		return o.Store.HookProgress(progress)
	}
	stepID := ""
	if progress.Step.ID != "" {
		stepID = StepID(progress.ExecutionID, progress.Step.ID, progress.Visit)
	}
	message := progress.Kind
	if progress.Message != "" {
		message += ": " + progress.Message
	}
	if progress.Error != "" {
		message += ": " + progress.Error
	}
	record, err := o.Store.Apply(progress.ExecutionID, progress.Kind, message, stepID, func(record *Execution) (bool, error) {
		// Cancellation is authoritative. Late worker events must not revive it.
		if record.State == execution.ExecutionCancelled {
			return false, nil
		}
		if progress.Kind == "execution_completed" {
			if record.State == execution.ExecutionCompleted {
				return false, nil
			}
			record.State = execution.ExecutionCompleted
			record.CurrentStepID = ""
			finishLifecycle(record, "completed", time.Now().UTC())
			return true, nil
		}
		if progress.Kind == "execution_failed" {
			if record.State == execution.ExecutionFailed && record.Error == progress.Error {
				return false, nil
			}
			record.State = execution.ExecutionFailed
			for i := range record.Steps {
				visit := &record.Steps[i]
				if visit.State == "running" || visit.State == "waiting_peer" || visit.State == "waiting_input" {
					visit.State = "failed"
					if visit.Error == "" {
						visit.Error = progress.Error
					}
					now := time.Now().UTC()
					visit.FinishedAt = &now
				}
			}
			record.Error = progress.Error
			finishLifecycle(record, "failed", time.Now().UTC())
			return true, nil
		}
		if progress.Kind == "stage_started" || progress.Kind == "stage_completed" {
			return recordStage(record, progress, time.Now().UTC())
		}
		if progress.Step.ID == "" {
			return false, fmt.Errorf("%s needs a step", progress.Kind)
		}
		index := -1
		for i := range record.Steps {
			if record.Steps[i].ID == stepID {
				index = i
				break
			}
		}
		if progress.Kind == "step_started" {
			if index >= 0 {
				return false, nil
			}
			record.Steps = append(record.Steps, Step{ID: stepID, Key: progress.Step.ID,
				Stage: progress.Step.Stage, Title: progress.Step.Title, Visit: progress.Visit, State: "running", StartedAt: time.Now().UTC()})
			record.State = execution.ExecutionRunning
			record.CurrentStepID = stepID
			return true, nil
		}
		if index < 0 {
			return false, fmt.Errorf("step %s was not recorded before %s", stepID, progress.Kind)
		}
		step := &record.Steps[index]
		if changed, handled, err := recordAttempt(step, progress, time.Now().UTC()); handled {
			return changed, err
		}
		switch progress.Kind {
		case "stage_round_interrupted":
			record.State = execution.ExecutionBlocked
			for i := range record.Steps {
				visit := &record.Steps[i]
				if visit.Visit != progress.Visit || visit.State == "completed" {
					continue
				}
				for _, configured := range record.Configuration.Workflow.Steps {
					if configured.ID == visit.Key && configured.Stage == progress.Step.Stage {
						visit.State = "waiting_peer"
						visit.Error = progress.Message
					}
				}
			}
			return true, nil
		case "stage_limit_reached":
			for _, event := range record.Events {
				if event.Kind == progress.Kind && event.StepID == stepID {
					return false, nil
				}
			}
			return true, nil
		case "step_blocked":
			if step.State == "completed" && step.Try >= progress.Try {
				return false, nil
			}
			if step.State == "blocked" && step.Error == progress.Error {
				return false, nil
			}
			step.State = "blocked"
			step.Error = progress.Error
			record.State = execution.ExecutionBlocked
			return true, nil
		case "decision_requested":
			requestID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(stepID+"/decision")).String()
			for _, request := range record.Requests {
				if request.ID == requestID {
					return false, nil
				}
			}
			prompt := progress.Step.Decision.Prompt
			if progress.Message != "" {
				prompt = progress.Message + " " + prompt
			}
			record.Requests = append(record.Requests, Request{ID: requestID,
				ExecutionID: record.ID, StepID: stepID, Prompt: prompt,
				Options: progress.Step.Decision.Options, State: "pending", CreatedAt: time.Now().UTC()})
			record.State = execution.ExecutionWaitingInput
			step.State = "waiting_input"
			return true, nil
		case "step_completed":
			if progress.Result == nil {
				return false, fmt.Errorf("completed step needs result")
			}
			if step.State == "completed" {
				return false, nil
			}
			step.State = "completed"
			step.Result = progress.Result
			step.Outcome = progress.Result.Outcome
			step.Try = progress.Try
			now := time.Now().UTC()
			step.FinishedAt = &now
			record.State = execution.ExecutionRunning
			return true, nil
		default:
			return false, fmt.Errorf("unknown progress kind %q", progress.Kind)
		}
	})
	if err == nil && o.Comms != nil && (progress.Kind == "execution_completed" || progress.Kind == "execution_failed") {
		err = o.Comms.FinishExecution(context.Background(), progress.ExecutionID)
	}
	if err != nil || (progress.Kind != "execution_failed" && progress.Kind != "stage_round_interrupted") || o.Sessions == nil {
		return err
	}
	active, err := o.Sessions.List(progress.ExecutionID)
	if err != nil {
		return fmt.Errorf("list sessions for failed execution: %w", err)
	}
	var stopErrors []error
	for _, session := range active {
		if progress.Kind == "stage_round_interrupted" {
			included := false
			for _, visit := range record.Steps {
				if visit.ID == session.StepID && visit.Visit == progress.Visit {
					for _, configured := range record.Configuration.Workflow.Steps {
						if configured.ID == visit.Key && configured.Stage == progress.Step.Stage {
							included = true
						}
					}
				}
			}
			if !included {
				continue
			}
		}
		if session.State == sessions.Running {
			if err := o.Sessions.Stop(session.ID); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("stop session %s: %w", session.ID, err))
			}
		}
	}
	return errors.Join(stopErrors...)
}

func (s *Store) AttachSession(executionID, stepID, sessionID string) error {
	_, err := s.Apply(executionID, "session_started", "Agent session started: "+sessionID, stepID,
		func(record *Execution) (bool, error) {
			for i := range record.Steps {
				step := &record.Steps[i]
				if step.ID != stepID {
					continue
				}
				for _, existing := range step.Sessions {
					if existing == sessionID {
						return false, nil
					}
				}
				step.Sessions = append(step.Sessions, sessionID)
				return true, nil
			}
			return false, fmt.Errorf("unknown step %s", stepID)
		})
	return err
}
