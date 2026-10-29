package hatchetengine

import (
	"fmt"
	"strings"

	"controls/execution"

	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

type memberResult struct {
	index  int
	result execution.StepResult
	err    error
}

// Every launch is allocated in a fixed order through RunMany before waiting.
// Result subscriptions may wait together: their durable branch IDs are already
// allocated, so arrival order never allocates replay-sensitive engine actions.
func (e *Engine) runConcurrent(ctx hatchet.DurableContext, input Input, stage execution.Stage, steps map[execution.StepID]execution.Step, packages map[string]execution.PackageSnapshot, visits map[execution.StepID]uint, history []CompletedStep) ([]CompletedStep, execution.StepID, error) {
	for _, id := range stage.Steps {
		visits[id]++
		if err := e.record(ctx, Progress{Kind: "step_started", ExecutionID: input.ExecutionID, Step: steps[id], Visit: visits[id]}); err != nil {
			return nil, "", err
		}
	}
	for attempt := uint(1); ; attempt++ {
		peers := make([]execution.Peer, 0, len(stage.Steps))
		for _, id := range stage.Steps {
			peers = append(peers, execution.Peer{Step: id, Name: string(id), Visit: visits[id], Try: attempt})
		}
		inputs := make([]hatchet.RunManyOpt, 0, len(peers))
		for _, peer := range peers {
			step := steps[peer.Step]
			if err := e.record(ctx, Progress{Kind: "attempt_started", ExecutionID: input.ExecutionID, Step: step, Visit: peer.Visit, Try: attempt}); err != nil {
				return nil, "", err
			}
			inputs = append(inputs, hatchet.RunManyOpt{Input: Work{ExecutionID: input.ExecutionID, Project: input.Project, Brief: input.Brief, Inputs: input.Inputs, Step: step, Package: packages[step.Run.Package], Visit: peer.Visit, Try: attempt, History: history, Peers: peers}})
		}
		refs, err := e.packageTask.RunMany(ctx, inputs)
		if err != nil {
			return nil, "", err
		}
		arrivals := make(chan memberResult, len(refs))
		for i := range refs {
			go func() {
				output, err := refs[i].Result()
				var result execution.StepResult
				if err == nil {
					err = output.TaskOutput(packageTaskName).Into(&result)
				}
				if err == nil {
					err = validateResult(steps[peers[i].Step], result)
				}
				arrivals <- memberResult{i, result, err}
			}()
		}
		results := make([]memberResult, len(refs))
		interrupted := false
		for range refs {
			received := <-arrivals
			results[received.index] = received
			peer := peers[received.index]
			step := steps[peer.Step]
			terminalFailure := received.err == nil && step.Routes[received.result.Outcome] == execution.Fail
			if !interrupted && (received.err != nil || terminalFailure) {
				// This observer operation only fences/stops supervised processes. Durable
				// retry decisions below use fixed member order, never completion timing.
				message := "Concurrent round interrupted by " + string(step.ID)
				if received.err != nil {
					message += ": " + received.err.Error()
				}
				if err = e.record(ctx, Progress{Kind: "stage_round_interrupted", ExecutionID: input.ExecutionID, Step: step, Visit: peer.Visit, Try: attempt, Message: message}); err != nil {
					return nil, "", err
				}
				interrupted = true
			}
		}
		completed := []CompletedStep{}
		byStep := map[execution.StepID]execution.StepResult{}
		firstFailure := -1
		failures := []string{}
		terminalFailure := false
		for i, received := range results {
			peer := peers[i]
			step := steps[peer.Step]
			if received.err != nil {
				if isDurableReplayError(received.err) {
					return nil, "", received.err
				}
				if firstFailure < 0 {
					firstFailure = i
				}
				failures = append(failures, fmt.Sprintf("%s: %v", step.ID, received.err))
				if err = e.record(ctx, Progress{Kind: "attempt_failed", ExecutionID: input.ExecutionID, Step: step, Visit: peer.Visit, Try: attempt, Error: received.err.Error()}); err != nil {
					return nil, "", err
				}
			} else {
				if step.Routes[received.result.Outcome] == execution.Fail {
					terminalFailure = true
				}
				byStep[step.ID] = received.result
				completed = append(completed, CompletedStep{Step: step.ID, Visit: peer.Visit, Try: attempt, Result: received.result})
				if err = e.record(ctx, Progress{Kind: "attempt_completed", ExecutionID: input.ExecutionID, Step: step, Visit: peer.Visit, Try: attempt, Result: &received.result}); err != nil {
					return nil, "", err
				}
			}
		}
		if terminalFailure {
			for _, member := range completed {
				result := member.Result
				if err = e.record(ctx, Progress{Kind: "step_completed", ExecutionID: input.ExecutionID, Step: steps[member.Step], Visit: member.Visit, Try: member.Try, Result: &result}); err != nil {
					return nil, "", err
				}
			}
			return completed, execution.Fail, nil
		}
		if firstFailure >= 0 {
			peer := peers[firstFailure]
			step := steps[peer.Step]
			message := strings.Join(failures, "; ")
			if e.observer == nil {
				return completed, "", fmt.Errorf("concurrent round: %s", message)
			}
			if err = e.record(ctx, Progress{Kind: "step_blocked", ExecutionID: input.ExecutionID, Step: step, Visit: peer.Visit, Try: attempt, Error: "Retry restarts every member of this round. " + message}); err != nil {
				return nil, "", err
			}
			if err = waitForRetry(ctx, input.ExecutionID, step.ID, peer.Visit, attempt, input.StartedAt); err != nil {
				return nil, "", err
			}
			continue
		}
		for _, member := range completed {
			step := steps[member.Step]
			result := member.Result
			if err = e.record(ctx, Progress{Kind: "step_completed", ExecutionID: input.ExecutionID, Step: step, Visit: member.Visit, Try: member.Try, Result: &result}); err != nil {
				return nil, "", err
			}
		}
		target, err := execution.ConcurrentTarget(stage, steps, byStep)
		return completed, target, err
	}
}
