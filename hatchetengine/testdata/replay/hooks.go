package main

import (
	"context"
	"fmt"
	"time"

	"controls/execution"
	"controls/hatchetengine"

	"github.com/google/uuid"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (r *runner) RunHook(_ context.Context, work execution.HookWork) (execution.HookRun, error) {
	r.hookCalls.Add(1)
	r.hooksMu.Lock()
	r.hookContexts = append(r.hookContexts, work.Context)
	r.hooksMu.Unlock()
	run := execution.HookRun{ID: work.Context.InvocationID, Context: work.Context, Try: work.Try, State: "succeeded"}
	if work.Hook.Name == "retry-me" && work.Try == 1 {
		run.State = "failed"
		run.Error = "fixture hook failure"
	}
	return run, nil
}

func testHooks(name string) execution.Hooks {
	return execution.Hooks{Before: []execution.Hook{{Name: name + "-before", Command: "fixture"}}, After: []execution.Hook{{Name: name + "-after", Command: "fixture"}}}
}

func checkHooks(ctx context.Context, engine *hatchetengine.Engine, progress <-chan hatchetengine.Progress, r *runner, client *hatchet.Client) error {
	for _, mode := range []string{"sequential", "concurrent"} {
		configuration := execution.Configuration{Hooks: testHooks("execution"), Workflow: execution.Workflow{Name: "hook-lifecycle", Start: "build"}, Packages: []execution.PackageSnapshot{{Name: "test", Digest: "fixture", Runner: "test"}}}
		configuration.Workflow.Stages = []execution.Stage{{ID: "work", Steps: []execution.StepID{"build", "review"}, Loop: &execution.LoopPolicy{Mode: mode, Limit: 2}, Human: &execution.HumanPolicy{Required: true, Step: "approval"}, Hooks: testHooks("stage")}}
		build := execution.Step{ID: "build", Stage: "work", Kind: execution.RunStep, Run: &execution.RunSpec{Package: "test"}, Hooks: testHooks("build"), Routes: map[execution.Outcome]execution.StepID{"done": "review"}}
		if mode == "concurrent" {
			build.Routes["done"] = "approval"
		}
		review := execution.Step{ID: "review", Stage: "work", Kind: execution.RunStep, Run: &execution.RunSpec{Package: "test"}, Hooks: testHooks("review"), Routes: map[execution.Outcome]execution.StepID{"done": "approval", "again": "build"}}
		approval := execution.Step{ID: "approval", Stage: "work", Kind: execution.DecisionStep, Decision: &execution.DecisionSpec{Authority: "human", Prompt: "approve?", Options: []execution.Outcome{"approve"}}, Routes: map[execution.Outcome]execution.StepID{"approve": execution.Complete}}
		configuration.Workflow.Steps = []execution.Step{build, review, approval}
		before := r.hookCalls.Load()
		started, err := engine.Start(ctx, execution.StartRequest{ExecutionID: uuid.NewString(), Project: "/tmp", Actor: "test", RequestID: uuid.NewString(), Configuration: configuration})
		if err != nil {
			return err
		}
		if err := waitForProgress(ctx, progress, "decision_requested", "approval"); err != nil {
			return err
		}
		if got := r.hookCalls.Load() - before; got != 10 {
			return fmt.Errorf("hooks before human gate: got %d want 10", got)
		}
		if err := engine.SendDecision(ctx, execution.Decision{ExecutionID: started.ExecutionID, Step: "approval", Visit: 1, Outcome: "approve", Actor: "human", Reason: "approved", At: time.Now().UTC()}); err != nil {
			return err
		}
		if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
			return err
		}
		if got := r.hookCalls.Load() - before; got != 12 {
			return fmt.Errorf("hooks after gate: got %d want 12", got)
		}
		r.hooksMu.Lock()
		var stage execution.HookContext
		for _, scope := range r.hookContexts {
			if scope.ExecutionID == started.ExecutionID && scope.Scope == "stage" && scope.Boundary == "after" {
				stage = scope
			}
		}
		r.hooksMu.Unlock()
		if len(stage.Participants) != 4 {
			return fmt.Errorf("stage attribution got %d agents", len(stage.Participants))
		}
		if err := waitForCommittedRun(ctx, client, started.RunID); err != nil {
			return err
		}
		id, _ := uuid.Parse(started.RunID)
		ids := []openapi_types.UUID{openapi_types.UUID(id)}
		if _, err := client.Runs().Replay(ctx, rest.V1ReplayTaskRequest{ExternalIds: &ids}); err != nil {
			return err
		}
		if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
			return err
		}
		if got := r.hookCalls.Load() - before; got != 12 {
			return fmt.Errorf("replay repeated hooks: %d", got)
		}
	}
	configuration := workflow()
	configuration.Hooks = execution.Hooks{Before: []execution.Hook{{Name: "retry-me", Command: "fixture"}}}
	before := r.calls.Load()
	started, err := engine.Start(ctx, execution.StartRequest{ExecutionID: uuid.NewString(), Project: "/tmp", Actor: "test", RequestID: uuid.NewString(), Configuration: configuration})
	if err != nil {
		return err
	}
	var blocked *execution.HookRun
	for blocked == nil {
		select {
		case event := <-progress:
			if event.Kind == "hook_blocked" {
				blocked = event.Hook
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.calls.Load() != before {
		return fmt.Errorf("agent ran before failed execution hook recovered")
	}
	if err := engine.SendRetry(ctx, execution.Retry{ExecutionID: started.ExecutionID, Step: execution.StepID(blocked.ID), Visit: 1, Try: blocked.Try, Actor: "test", Reason: "retry hook only"}); err != nil {
		return err
	}
	if err := waitForProgress(ctx, progress, "decision_requested", "design"); err != nil {
		return err
	}
	configuration = execution.Configuration{Hooks: execution.Hooks{After: []execution.Hook{{Name: "first", Command: "fixture"}, {Name: "retry-me", Command: "fixture"}, {Name: "last", Command: "fixture"}}}, Workflow: execution.Workflow{Name: "after-retry", Start: "work", Steps: []execution.Step{{ID: "work", Kind: execution.RunStep, Run: &execution.RunSpec{Package: "test"}, Routes: map[execution.Outcome]execution.StepID{"done": execution.Complete}}}}, Packages: []execution.PackageSnapshot{{Name: "test", Digest: "fixture", Runner: "test"}}}
	before = r.calls.Load()
	after, err := engine.Start(ctx, execution.StartRequest{ExecutionID: uuid.NewString(), Project: "/tmp", Actor: "test", RequestID: uuid.NewString(), Configuration: configuration})
	if err != nil {
		return err
	}
	blocked = nil
	for blocked == nil {
		select {
		case event := <-progress:
			if event.ExecutionID == after.ExecutionID && event.Kind == "hook_blocked" {
				blocked = event.Hook
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.calls.Load() != before+1 {
		return fmt.Errorf("expected one completed agent before the after hook")
	}
	if err := engine.SendRetry(ctx, execution.Retry{ExecutionID: after.ExecutionID, Step: execution.StepID(blocked.ID), Visit: 1, Try: blocked.Try, Actor: "test", Reason: "recover after hook"}); err != nil {
		return err
	}
	if err := waitForProgress(ctx, progress, "execution_completed", ""); err != nil {
		return err
	}
	if r.calls.Load() != before+1 {
		return fmt.Errorf("hook retry reran a completed agent")
	}
	return engine.Cancel(ctx, started.RunID)
}
