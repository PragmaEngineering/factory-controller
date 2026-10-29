package hatchetengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controls/execution"

	hatchetclient "github.com/hatchet-dev/hatchet/pkg/client"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func (e *Engine) run(ctx hatchet.DurableContext, input Input) (Output, error) {
	output, err := e.runGraph(ctx, input)
	kind := "execution_completed"
	message := ""
	if err != nil {
		if errors.Is(err, context.Canceled) && !input.StartedAt.IsZero() && time.Since(input.StartedAt) >= executionTimeout-time.Minute {
			err = fmt.Errorf("execution timed out after %s: %w", executionTimeout, err)
		}
		kind, message = "execution_failed", err.Error()
	}
	if recordErr := e.record(ctx, Progress{Kind: kind, ExecutionID: input.ExecutionID, Error: message, History: output.History}); recordErr != nil {
		return output, errors.Join(err, recordErr)
	}
	return output, err
}

func (e *Engine) runGraph(ctx hatchet.DurableContext, input Input) (Output, error) {
	steps := make(map[execution.StepID]execution.Step, len(input.Configuration.Workflow.Steps))
	packages := make(map[string]execution.PackageSnapshot, len(input.Configuration.Packages))
	for _, step := range input.Configuration.Workflow.Steps {
		steps[step.ID] = step
	}
	for _, pkg := range input.Configuration.Packages {
		packages[pkg.Name] = pkg
	}

	output := Output{ExecutionID: input.ExecutionID}
	visits := make(map[execution.StepID]uint)
	stageRouter := execution.NewStageRouter(input.Configuration.Workflow)
	current := input.Configuration.Workflow.Start
	escalation := ""
	for {
		var concurrent *execution.Stage
		for _, candidate := range input.Configuration.Workflow.Stages {
			if candidate.Concurrent() && candidate.Steps[0] == current {
				copy := candidate
				concurrent = &copy
				break
			}
		}
		if concurrent != nil {
			completed, target, err := e.runConcurrent(ctx, input, *concurrent, steps, packages, visits, output.History)
			output.History = append(output.History, completed...)
			if err != nil {
				return output, err
			}
			last := steps[concurrent.Steps[len(concurrent.Steps)-1]]
			next, limited, err := stageRouter.Next(last.ID, target)
			if err != nil {
				return output, err
			}
			if limited {
				escalation = fmt.Sprintf("Stage %s reached its %d-round limit.", concurrent.ID, concurrent.Loop.Limit)
				if err = e.record(ctx, Progress{Kind: "stage_limit_reached", ExecutionID: input.ExecutionID, Step: last, Visit: visits[last.ID], Message: escalation}); err != nil {
					return output, err
				}
			}
			switch next {
			case execution.Complete:
				return output, nil
			case execution.Fail:
				causes := []string{}
				for _, member := range completed {
					if steps[member.Step].Routes[member.Result.Outcome] == execution.Fail {
						causes = append(causes, fmt.Sprintf("%s: %s", member.Step, member.Result.Summary))
					}
				}
				return output, fmt.Errorf("concurrent stage %s routed to failure: %s", concurrent.ID, strings.Join(causes, "; "))
			default:
				current = next
			}
			continue
		}
		step := steps[current]
		visits[current]++
		visit := visits[current]
		if err := e.record(ctx, Progress{Kind: "step_started", ExecutionID: input.ExecutionID, Step: step, Visit: visit}); err != nil {
			return output, err
		}

		var result execution.StepResult
		var attempt uint = 1
		switch step.Kind {
		case execution.RunStep:
			var err error
			result, attempt, err = e.runPackage(ctx, input, step, packages[step.Run.Package], visit, output.History)
			if err != nil {
				return output, fmt.Errorf("step %s visit %d: %w", step.ID, visit, err)
			}
		case execution.DecisionStep:
			var err error
			if err := e.record(ctx, Progress{Kind: "decision_requested", ExecutionID: input.ExecutionID, Step: step, Visit: visit, Message: escalation}); err != nil {
				return output, err
			}
			escalation = ""
			result, err = waitForDecision(ctx, input, step, visit)
			if err != nil {
				return output, fmt.Errorf("decision %s visit %d: %w", step.ID, visit, err)
			}
		default:
			return output, fmt.Errorf("unsupported step kind %q", step.Kind)
		}

		next, ok := step.Routes[result.Outcome]
		if !ok {
			return output, fmt.Errorf("step %s returned outcome %q without a route", step.ID, result.Outcome)
		}
		configuredTarget := next
		output.History = append(output.History, CompletedStep{
			Step: step.ID, Visit: visit, Try: attempt, Result: result,
		})
		if err := e.record(ctx, Progress{Kind: "step_completed", ExecutionID: input.ExecutionID, Step: step, Visit: visit, Try: attempt, Result: &result}); err != nil {
			return output, err
		}
		next, limitReached, err := stageRouter.Next(step.ID, next)
		if err != nil {
			return output, err
		}
		if limitReached {
			escalation = fmt.Sprintf("Stage %s reached its %d-round limit with unresolved findings. Review the latest result and artifacts before deciding.", step.Stage, stageRouter.Limit(step.Stage))
			if err := e.record(ctx, Progress{Kind: "stage_limit_reached", ExecutionID: input.ExecutionID,
				Step: step, Visit: visit, Message: fmt.Sprintf("%s Route to %s replaced %s.", escalation, next, configuredTarget)}); err != nil {
				return output, err
			}
		}
		switch next {
		case execution.Complete:
			return output, nil
		case execution.Fail:
			return output, fmt.Errorf("step %s routed to failure: %s", step.ID, result.Summary)
		default:
			current = next
		}
	}
}

func (e *Engine) runPackage(
	ctx hatchet.DurableContext,
	input Input,
	step execution.Step,
	pkg execution.PackageSnapshot,
	visit uint,
	history []CompletedStep,
) (execution.StepResult, uint, error) {
	maxAttempts := step.Retry.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	for attempt := uint(1); ; attempt++ {
		if err := e.record(ctx, Progress{Kind: "attempt_started", ExecutionID: input.ExecutionID, Step: step, Visit: visit, Try: attempt}); err != nil {
			return execution.StepResult{}, attempt, err
		}
		work := Work{
			ExecutionID: input.ExecutionID,
			Project:     input.Project,
			Brief:       input.Brief,
			Inputs:      input.Inputs,
			Step:        step,
			Package:     pkg,
			Visit:       visit,
			Try:         attempt,
			History:     history,
		}
		child, attemptErr := e.packageTask.Run(ctx, work)
		if attemptErr == nil {
			var result execution.StepResult
			if err := child.Into(&result); err != nil {
				attemptErr = fmt.Errorf("decode package result: %w", err)
			} else if err := validateResult(step, result); err != nil {
				attemptErr = err
			} else {
				if err := e.record(ctx, Progress{Kind: "attempt_completed", ExecutionID: input.ExecutionID, Step: step, Visit: visit, Try: attempt, Result: &result}); err != nil {
					return execution.StepResult{}, attempt, err
				}
				if attempt < maxAttempts && containsOutcome(step.Retry.Outcomes, result.Outcome) {
					if err := retryDelay(ctx, step.Retry.DelaySeconds); err != nil {
						return result, attempt, err
					}
					continue
				}
				return result, attempt, nil
			}
		}
		if isDurableReplayError(attemptErr) {
			return execution.StepResult{}, attempt, fmt.Errorf("durable workflow replay failed: %w", attemptErr)
		}
		if err := e.record(ctx, Progress{Kind: "attempt_failed", ExecutionID: input.ExecutionID, Step: step, Visit: visit, Try: attempt, Error: attemptErr.Error()}); err != nil {
			return execution.StepResult{}, attempt, err
		}
		if errors.Is(attemptErr, context.Canceled) {
			return execution.StepResult{}, attempt, attemptErr
		}
		if attempt < maxAttempts && containsFailure(step.Retry.Failures, failureKind(attemptErr)) {
			if err := retryDelay(ctx, step.Retry.DelaySeconds); err != nil {
				return execution.StepResult{}, attempt, err
			}
			continue
		}
		if e.observer == nil {
			return execution.StepResult{}, attempt, attemptErr
		}
		if err := e.record(ctx, Progress{Kind: "step_blocked", ExecutionID: input.ExecutionID, Step: step, Visit: visit, Try: attempt, Error: attemptErr.Error()}); err != nil {
			return execution.StepResult{}, attempt, err
		}
		if err := waitForRetry(ctx, input.ExecutionID, step.ID, visit, attempt, input.StartedAt); err != nil {
			return execution.StepResult{}, attempt, err
		}
	}
}

func waitForRetry(ctx hatchet.DurableContext, executionID string, step execution.StepID, visit, attempt uint, since time.Time) error {
	scope := retryScope(executionID, step, visit, attempt)
	event, err := ctx.WaitForEvent(retryEventName, "", hatchet.WithEventScope(scope), hatchet.WithConsiderEventsSince(since))
	if err != nil {
		return err
	}
	var retry Retry
	if err := hatchet.EventInto(event, &retry); err != nil {
		return err
	}
	if retry.ExecutionID != executionID || retry.Step != step || retry.Visit != visit || retry.Try != attempt || retry.Actor == "" {
		return errors.New("retry event does not match blocked attempt")
	}
	return nil
}

func validateResult(step execution.Step, result execution.StepResult) error {
	if result.Outcome == "" || result.Summary == "" || result.Actor == "" || result.At.IsZero() {
		return errors.New("package result requires outcome, summary, actor, and timestamp")
	}
	if _, exists := step.Routes[result.Outcome]; !exists {
		return fmt.Errorf("package outcome %q has no route", result.Outcome)
	}
	if !step.RequiresOutputs(result.Outcome) {
		return nil
	}
	for _, required := range step.Outputs {
		found := false
		for _, artifact := range result.Artifacts {
			if artifact.Name == required && artifact.URI != "" && artifact.Digest != "" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("required output %q is missing an artifact URI or digest", required)
		}
	}
	return nil
}

func waitForDecision(ctx hatchet.DurableContext, input Input, step execution.Step, visit uint) (execution.StepResult, error) {
	scope := decisionScope(input.ExecutionID, step.ID, visit)
	event, err := ctx.WaitForEvent(decisionEventName, "",
		hatchet.WithEventScope(scope),
		hatchet.WithConsiderEventsSince(input.StartedAt),
	)
	if err != nil {
		return execution.StepResult{}, err
	}
	var decision Decision
	if err := hatchet.EventInto(event, &decision); err != nil {
		return execution.StepResult{}, fmt.Errorf("decode decision: %w", err)
	}
	if decision.ExecutionID != input.ExecutionID || decision.Step != step.ID || decision.Visit != visit {
		return execution.StepResult{}, errors.New("decision does not match the active step")
	}
	if !containsOutcome(step.Decision.Options, decision.Outcome) || decision.Actor == "" || decision.Reason == "" || decision.At.IsZero() {
		return execution.StepResult{}, errors.New("decision has an invalid outcome, actor, reason, or timestamp")
	}
	return execution.StepResult{
		Outcome: decision.Outcome,
		Summary: decision.Reason,
		Actor:   decision.Actor,
		At:      decision.At,
	}, nil
}

func isDurableReplayError(err error) bool {
	var sdkError *hatchet.NonDeterminismError
	var clientError *hatchetclient.NonDeterminismError
	return errors.As(err, &sdkError) || errors.As(err, &clientError)
}

func retryDelay(ctx hatchet.DurableContext, seconds uint) error {
	if seconds == 0 {
		return nil
	}
	_, err := ctx.SleepFor(time.Duration(seconds) * time.Second)
	return err
}

func containsOutcome(outcomes []execution.Outcome, outcome execution.Outcome) bool {
	for _, candidate := range outcomes {
		if candidate == outcome {
			return true
		}
	}
	return false
}

func containsFailure(failures []execution.FailureKind, failure execution.FailureKind) bool {
	for _, candidate := range failures {
		if candidate == failure {
			return true
		}
	}
	return false
}

func failureKind(err error) execution.FailureKind {
	if errors.Is(err, context.DeadlineExceeded) {
		return execution.FailureTimeout
	}
	return execution.FailureExit
}
