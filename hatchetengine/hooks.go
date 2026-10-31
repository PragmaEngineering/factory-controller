package hatchetengine

import (
	"fmt"

	"controls/execution"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func hookContext(input Input, scope, boundary string) execution.HookContext {
	return execution.HookContext{Version: 1, ExecutionID: input.ExecutionID, Project: input.Project, Scope: scope, Boundary: boundary}
}

func participants(input Input, history []CompletedStep) []execution.HookParticipant {
	var people []execution.HookParticipant
	for _, completed := range history {
		for _, step := range input.Configuration.Workflow.Steps {
			if step.ID == completed.Step && step.Run != nil {
				people = append(people, execution.HookParticipant{Agent: step.Run.Package, InstanceID: completed.Result.Actor, Step: completed.Step, Visit: completed.Visit, Attempt: completed.Try, Outcome: completed.Result.Outcome})
			}
		}
	}
	return people
}

func (e *Engine) runHooks(ctx hatchet.DurableContext, input Input, hooks []execution.Hook, scope execution.HookContext) error {
	for _, hook := range hooks {
		assigned := scope
		assigned.Name = hook.Name
		assigned.InvocationID = assigned.ID()
		for attempt := uint(1); ; attempt++ {
			run := execution.HookRun{ID: assigned.InvocationID, Context: assigned, Try: attempt, State: "pending"}
			if err := e.record(ctx, Progress{Kind: "hook_pending", ExecutionID: input.ExecutionID, Hook: &run}); err != nil {
				return err
			}
			child, err := e.hookTask.Run(ctx, execution.HookWork{Hook: hook, Context: assigned, Try: attempt})
			if err == nil {
				err = child.Into(&run)
			}
			if err == nil && run.State == "succeeded" {
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isDurableReplayError(err) {
				return fmt.Errorf("durable hook replay failed: %w", err)
			}
			// Preserve the addressable invocation even when the host failed before
			// returning a structured result. External effects may already have happened.
			run.ID = assigned.InvocationID
			run.Context = assigned
			run.Try = attempt
			run.State = "blocked"
			if err != nil {
				run.Error = err.Error()
			}
			if run.Error == "" {
				run.Error = "hook did not report successful completion"
			}
			if err := e.record(ctx, Progress{Kind: "hook_blocked", ExecutionID: input.ExecutionID, Hook: &run}); err != nil {
				return err
			}
			if e.observer == nil {
				return fmt.Errorf("hook %s: %s", hook.Name, run.Error)
			}
			if err := waitForRetry(ctx, input.ExecutionID, execution.StepID(run.ID), 1, attempt, input.StartedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) stepHooks(ctx hatchet.DurableContext, input Input, step execution.Step, visit, attempt uint, boundary string, history []CompletedStep) error {
	scope := hookContext(input, "step", boundary)
	scope.Stage = step.Stage
	scope.Step = step.ID
	scope.StepVisit = visit
	scope.StepAttempt = attempt
	scope.Participants = participants(input, history)
	if len(history) > 0 {
		scope.Outcome = history[len(history)-1].Result.Outcome
	}
	list := step.Hooks.Before
	if boundary == "after" {
		list = step.Hooks.After
	}
	return e.runHooks(ctx, input, list, scope)
}

// stageHooks tracks entries, not loop rounds. Human feedback routed inside the
// same stage does not close it. Returning from another stage is a new entry.
type stageHooks struct {
	active string
	visits map[string]uint
	start  int
}

func (s *stageHooks) enter(e *Engine, ctx hatchet.DurableContext, input Input, step execution.Step, history []CompletedStep) error {
	if step.Stage == s.active {
		return nil
	}
	s.active = step.Stage
	s.start = len(history)
	if s.active == "" {
		return nil
	}
	if s.visits == nil {
		s.visits = map[string]uint{}
	}
	s.visits[s.active]++
	if err := e.record(ctx, Progress{Kind: "stage_started", ExecutionID: input.ExecutionID, Stage: s.active, StageVisit: s.visits[s.active]}); err != nil {
		return err
	}
	scope := hookContext(input, "stage", "before")
	scope.Stage = s.active
	scope.StageVisit = s.visits[s.active]
	for _, stage := range input.Configuration.Workflow.Stages {
		if stage.ID == s.active {
			return e.runHooks(ctx, input, stage.Hooks.Before, scope)
		}
	}
	return nil
}

func (s *stageHooks) leave(e *Engine, ctx hatchet.DurableContext, input Input, next execution.StepID, steps map[execution.StepID]execution.Step, history []CompletedStep) error {
	if s.active == "" || next == execution.Fail || steps[next].Stage == s.active {
		return nil
	}
	scope := hookContext(input, "stage", "after")
	scope.Stage = s.active
	scope.StageVisit = s.visits[s.active]
	scope.Participants = participants(input, history[s.start:])
	for _, stage := range input.Configuration.Workflow.Stages {
		if stage.ID == s.active {
			if err := e.runHooks(ctx, input, stage.Hooks.After, scope); err != nil {
				return err
			}
		}
	}
	if err := e.record(ctx, Progress{Kind: "stage_completed", ExecutionID: input.ExecutionID, Stage: s.active, StageVisit: s.visits[s.active]}); err != nil {
		return err
	}
	s.active = ""
	return nil
}
