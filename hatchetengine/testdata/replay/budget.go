package main

import (
	"context"
	"controls/execution"
	"controls/hatchetengine"
	"fmt"
	"github.com/google/uuid"
	"time"
)

func checkBudgets(ctx context.Context, engine *hatchetengine.Engine, progress <-chan hatchetengine.Progress, r *runner) error {
	for _, mode := range []string{"sequential", "concurrent"} {
		for _, action := range []string{"pause", "stop"} {
			config := execution.Configuration{Workflow: execution.Workflow{Name: "budget-test", Start: "one"}, Packages: []execution.PackageSnapshot{{Name: "test", Digest: "fixture", Runner: "test"}}}
			ids := []execution.StepID{"one"}
			if mode == "concurrent" {
				ids = append(ids, "two")
			}
			for i, id := range ids {
				next := execution.Complete
				if mode != "concurrent" && i+1 < len(ids) {
					next = ids[i+1]
				}
				config.Workflow.Steps = append(config.Workflow.Steps, execution.Step{ID: id, Stage: "budget", Kind: execution.RunStep, Run: &execution.RunSpec{Package: "test"}, Budget: &execution.Budget{Tokens: 1, Action: action}, Retry: execution.RetryPolicy{MaxAttempts: 3, Failures: []execution.FailureKind{execution.FailureExit}}, Routes: map[execution.Outcome]execution.StepID{"done": next}})
			}
			if mode == "concurrent" {
				config.Workflow.Stages = []execution.Stage{{ID: "budget", Steps: ids, Loop: &execution.LoopPolicy{Mode: mode, Limit: 1}}}
			}
			before := r.calls.Load()
			started, err := engine.Start(ctx, execution.StartRequest{ExecutionID: uuid.NewString(), Project: "/tmp", Actor: "test", RequestID: uuid.NewString(), Configuration: config})
			if err != nil {
				return err
			}
			target := "step_blocked"
			if action == "stop" {
				target = "execution_failed"
			}
			var blocked hatchetengine.Progress
			for {
				select {
				case event := <-progress:
					if event.ExecutionID != started.ExecutionID {
						continue
					}
					if event.Kind == target {
						blocked = event
					}
					if event.Kind == "execution_completed" || (event.Kind == "execution_failed" && action != "stop") {
						return fmt.Errorf("%s budget %s unexpected %s", mode, action, event.Kind)
					}
				case <-ctx.Done():
					return ctx.Err()
				}
				if blocked.Kind != "" {
					break
				}
			}
			time.Sleep(200 * time.Millisecond)
			if got := r.calls.Load() - before; got != int32(len(ids)) {
				return fmt.Errorf("budget automatically retried: %d invocations", got)
			}
			if action == "pause" {
				if err := engine.SendRetry(ctx, execution.Retry{ExecutionID: started.ExecutionID, Step: blocked.Step.ID, Visit: blocked.Visit, Try: blocked.Try, Actor: "test", Reason: "explicit new attempt budget"}); err != nil {
					return err
				}
				if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
