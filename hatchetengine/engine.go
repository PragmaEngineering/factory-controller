// Package hatchetengine runs Factory workflows using Hatchet's durable tasks.
// It owns execution scheduling, not package isolation or caller authorization.
package hatchetengine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controls/execution"

	"github.com/google/uuid"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// PackageSlots is wired into host admission so a peer group reserves enough
// capacity before any member starts.
const PackageSlots = 10

const (
	executionTaskName = "factory-execution"
	packageTaskName   = "factory-package"
	hookTaskName      = "factory-hook"
	decisionEventName = "factory:decision"
	retryEventName    = "factory:retry"
	// A durable execution includes human decisions and explicit retry waits.
	// The engine's five-minute default is far too short for that lifecycle.
	executionTimeout = 365 * 24 * time.Hour
	// A package invocation is bounded separately so a stalled agent cannot
	// occupy a worker slot indefinitely.
	packageTimeout = 2 * time.Hour
)

type Runner = execution.Runner
type Observer = execution.Observer
type Progress = execution.Progress
type Work = execution.Work
type CompletedStep = execution.CompletedStep

type Input struct {
	ExecutionID   string                  `json:"execution_id"`
	Project       string                  `json:"project"`
	Brief         string                  `json:"brief"`
	Inputs        []execution.Artifact    `json:"inputs,omitempty"`
	Configuration execution.Configuration `json:"configuration"`
	StartedAt     time.Time               `json:"started_at"`
}

type Output struct {
	ExecutionID string          `json:"execution_id"`
	History     []CompletedStep `json:"history"`
}

type Started = execution.Started
type Decision = execution.Decision
type Retry = execution.Retry

// Engine registers a durable coordinator and a child task on a Hatchet client.
// One worker can handle many independent executions.
type Engine struct {
	client        *hatchet.Client
	runner        Runner
	observer      Observer
	executionTask *hatchet.StandaloneTask
	packageTask   *hatchet.StandaloneTask
	hookTask      *hatchet.StandaloneTask
}

func New(client *hatchet.Client, runner Runner, observers ...Observer) (*Engine, error) {
	if client == nil || runner == nil {
		return nil, errors.New("Hatchet client and package runner are required")
	}

	e := &Engine{client: client, runner: runner}
	if len(observers) > 0 {
		e.observer = observers[0]
	}
	e.packageTask = client.NewStandaloneTask(packageTaskName,
		func(ctx hatchet.Context, work Work) (execution.StepResult, error) {
			return e.runner.Run(ctx, work)
		},
		hatchet.WithExecutionTimeout(packageTimeout),
	)
	e.hookTask = client.NewStandaloneTask(hookTaskName, func(ctx hatchet.Context, work execution.HookWork) (execution.HookRun, error) {
		host, ok := runner.(execution.HookRunner)
		if !ok {
			return execution.HookRun{}, errors.New("host does not implement lifecycle hooks")
		}
		return host.RunHook(ctx, work)
	}, hatchet.WithExecutionTimeout(packageTimeout))
	e.executionTask = client.NewStandaloneDurableTask(executionTaskName,
		func(ctx hatchet.DurableContext, input Input) (Output, error) {
			return e.run(ctx, input)
		},
		hatchet.WithExecutionTimeout(executionTimeout),
	)
	return e, nil
}

func (e *Engine) record(ctx context.Context, progress Progress) error {
	if e.observer == nil {
		return nil
	}
	return e.observer.Record(ctx, progress)
}

// NewWorker registers execution, package, and hook tasks. The caller owns the worker lifecycle.
func (e *Engine) NewWorker(name string) (*hatchet.Worker, error) {
	return e.client.NewWorker(name,
		hatchet.WithWorkflows(e.executionTask, e.packageTask, e.hookTask),
		hatchet.WithSlots(PackageSlots),
		hatchet.WithDurableSlots(10),
	)
}

// Start submits an immutable configuration to Hatchet without waiting for the
// execution to finish. The returned Hatchet run ID is useful for monitoring.
func (e *Engine) Start(ctx context.Context, request execution.StartRequest) (Started, error) {
	if err := Validate(request.Configuration); err != nil {
		return Started{}, err
	}
	if request.Project == "" || request.Actor == "" || request.RequestID == "" {
		return Started{}, errors.New("project, actor, and request ID are required")
	}
	id := request.ExecutionID
	if id == "" {
		id = uuid.NewString()
	}
	input := Input{
		ExecutionID:   id,
		Project:       request.Project,
		Brief:         request.Brief,
		Inputs:        request.Inputs,
		Configuration: request.Configuration,
		StartedAt:     time.Now().UTC(),
	}
	ref, err := e.executionTask.RunNoWait(ctx, input,
		hatchet.WithRunMetadata(map[string]string{
			"factory_execution_id": input.ExecutionID,
			"factory_project":      input.Project,
			"factory_actor":        request.Actor,
			"factory_request_id":   request.RequestID,
		}),
	)
	if err != nil {
		return Started{}, fmt.Errorf("start Hatchet execution: %w", err)
	}
	return Started{ExecutionID: input.ExecutionID, RunID: ref.RunId}, nil
}

// SendDecision transports a previously authorized decision to the waiting
// durable task. It does not check authority or provide idempotent admission.
func (e *Engine) SendDecision(ctx context.Context, decision Decision) error {
	if decision.ExecutionID == "" || decision.Step == "" || decision.Visit == 0 || decision.Actor == "" || decision.Outcome == "" || decision.Reason == "" || decision.At.IsZero() {
		return errors.New("execution ID, step, visit, actor, outcome, reason, and timestamp are required")
	}
	scope := decisionScope(decision.ExecutionID, decision.Step, decision.Visit)
	return e.client.Events().Push(ctx, decisionEventName, decision,
		hatchet.WithFilterScope(&scope),
	)
}

func (e *Engine) Cancel(ctx context.Context, runID string) error {
	id, err := uuid.Parse(runID)
	if err != nil {
		return fmt.Errorf("invalid Hatchet run ID: %w", err)
	}
	ids := []openapi_types.UUID{openapi_types.UUID(id)}
	_, err = e.client.Runs().Cancel(ctx, rest.V1CancelTaskRequest{ExternalIds: &ids})
	return err
}

func (e *Engine) SendRetry(ctx context.Context, retry Retry) error {
	if retry.ExecutionID == "" || retry.Step == "" || retry.Visit == 0 || retry.Try == 0 || retry.Actor == "" {
		return errors.New("execution, step, visit, try, and actor are required")
	}
	scope := retryScope(retry.ExecutionID, retry.Step, retry.Visit, retry.Try)
	return e.client.Events().Push(ctx, retryEventName, retry, hatchet.WithFilterScope(&scope))
}

func decisionScope(executionID string, step execution.StepID, visit uint) string {
	return fmt.Sprintf("%s:%s:%d", executionID, step, visit)
}

func retryScope(executionID string, step execution.StepID, visit, attempt uint) string {
	return fmt.Sprintf("%s:%s:%d:%d", executionID, step, visit, attempt)
}
