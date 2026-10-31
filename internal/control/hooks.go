package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"controls/execution"
)

// ClaimHook fences duplicate task delivery before any external process starts.
// A claimed invocation with no session is ambiguous and must not be relaunched.
func (s *Store) ClaimHook(work execution.HookWork) (execution.HookRun, bool, error) {
	var result execution.HookRun
	claimed := false
	_, err := s.Apply(work.Context.ExecutionID, "hook_started", "Hook started: "+work.Context.Name, work.Context.InvocationID, func(record *Execution) (bool, error) {
		if record.State == execution.ExecutionCancelled {
			return false, errors.New("execution is cancelled")
		}
		for i := range record.Hooks {
			run := &record.Hooks[i]
			if run.ID != work.Context.InvocationID {
				continue
			}
			result = *run
			if run.State == "succeeded" || run.Try > work.Try || (run.Try == work.Try && run.State != "pending") {
				return false, nil
			}
			if run.State == "running" {
				return false, errors.New("previous hook attempt is still running")
			}
			now := time.Now().UTC()
			*run = execution.HookRun{ID: run.ID, Context: work.Context, Try: work.Try, State: "running", StartedAt: &now}
			result = *run
			claimed = true
			return true, nil
		}
		now := time.Now().UTC()
		result = execution.HookRun{ID: work.Context.InvocationID, Context: work.Context, Try: work.Try, State: "running", StartedAt: &now}
		record.Hooks = append(record.Hooks, result)
		claimed = true
		return true, nil
	})
	return result, claimed, err
}

func (s *Store) SaveHook(executionID string, run execution.HookRun) error {
	_, err := s.Apply(executionID, "hook_"+run.State, "Hook "+run.State+": "+run.Context.Name+" "+run.Error, run.ID, func(record *Execution) (bool, error) {
		if record.State == execution.ExecutionCancelled {
			return false, nil
		}
		for i := range record.Hooks {
			current := &record.Hooks[i]
			if current.ID != run.ID {
				continue
			}
			if current.Try != run.Try {
				return false, errors.New("hook attempt changed")
			}
			if current.State == "succeeded" {
				return false, nil
			}
			if run.SessionID == "" {
				run.SessionID = current.SessionID
			}
			if run.StartedAt == nil {
				run.StartedAt = current.StartedAt
			}
			if run.FinishedAt == nil {
				run.FinishedAt = current.FinishedAt
			}
			*current = run
			if run.State == "succeeded" && run.Context.Scope == "step" {
				record.CurrentStepID = execution.StepVisitID(executionID, run.Context.Step, run.Context.StepVisit)
			}
			return true, nil
		}
		return false, os.ErrNotExist
	})
	return err
}

func (s *Store) HookProgress(progress execution.Progress) error {
	run := *progress.Hook
	_, err := s.Apply(progress.ExecutionID, progress.Kind, "Hook "+run.State+": "+run.Context.Name+" "+run.Error, run.ID, func(record *Execution) (bool, error) {
		if record.State == execution.ExecutionCancelled {
			return false, nil
		}
		if progress.Kind == "hook_blocked" {
			record.State = execution.ExecutionBlocked
			record.CurrentStepID = run.ID
			record.Error = run.Error
		} else if progress.Kind == "hook_pending" {
			record.State = execution.ExecutionRunning
			record.CurrentStepID = run.ID
			record.Error = ""
		}
		for i := range record.Hooks {
			current := &record.Hooks[i]
			if current.ID != run.ID {
				continue
			}
			if current.State == "succeeded" || current.Try > run.Try {
				return false, nil
			}
			if progress.Kind == "hook_blocked" && current.State == "blocked" && current.Try == run.Try && current.Error == run.Error {
				return false, nil
			}
			// Retain the host's session and timing while the scheduler announces a retry.
			if progress.Kind == "hook_pending" && current.Try < run.Try {
				return true, nil
			}
			if progress.Kind == "hook_pending" && current.Try == run.Try {
				return false, nil
			}
			if run.SessionID == "" {
				run.SessionID = current.SessionID
			}
			if run.StartedAt == nil {
				run.StartedAt = current.StartedAt
			}
			if run.FinishedAt == nil {
				run.FinishedAt = current.FinishedAt
			}
			*current = run
			return true, nil
		}
		record.Hooks = append(record.Hooks, run)
		return true, nil
	})
	return err
}

func (c *Controller) retryHook(ctx context.Context, record Execution, id, actor, reason string) (Execution, error) {
	if c.Engine == nil {
		return Execution{}, errors.New("Factory execution service is not configured")
	}
	if actor == "" {
		return Execution{}, errors.New("retry actor is required")
	}
	var hook execution.HookRun
	for _, run := range record.Hooks {
		if run.ID == id {
			hook = run
			break
		}
	}
	if hook.State != "blocked" || record.State != execution.ExecutionBlocked || record.CurrentStepID != id {
		return Execution{}, fmt.Errorf("hook %s is %s; only a blocked hook can be retried", id, hook.State)
	}
	if reason == "" {
		reason = "operator retry"
	}
	_, err := c.Store.Apply(record.ID, "retry_requested", "Hook retry requested: "+reason, id, func(current *Execution) (bool, error) {
		if current.State == execution.ExecutionCancelled {
			return false, errors.New("execution is cancelled")
		}
		for i := range current.Hooks {
			run := &current.Hooks[i]
			if run.ID == id {
				if run.State != "blocked" {
					return false, errors.New("hook is no longer blocked")
				}
				run.State = "retrying"
				current.State = execution.ExecutionRunning
				return true, nil
			}
		}
		return false, os.ErrNotExist
	})
	if err != nil {
		return Execution{}, err
	}
	err = c.Engine.SendRetry(ctx, execution.Retry{ExecutionID: record.ID, Step: execution.StepID(id), Visit: 1, Try: hook.Try, Actor: actor, Reason: reason})
	if err != nil {
		_, _ = c.Store.Update(record.ID, "retry_delivery_failed", err.Error(), id, func(current *Execution) error {
			if current.State == execution.ExecutionCancelled {
				return errors.New("execution is cancelled")
			}
			for i := range current.Hooks {
				if current.Hooks[i].ID == id {
					current.Hooks[i].State = "blocked"
				}
			}
			current.State = execution.ExecutionBlocked
			return nil
		})
		return Execution{}, err
	}
	return c.Store.Get(record.ID)
}
