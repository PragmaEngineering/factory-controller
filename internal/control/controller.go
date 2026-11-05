package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"controls/adapters"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/factoryconfig"
	"controls/internal/inputs"
	"controls/internal/sessions"

	"github.com/google/uuid"
)

type Controller struct {
	Adapters adapters.Registry
	Comms    *comms.Service
	Store    *Store
	Sessions *sessions.Manager
	Engine   execution.Engine
	Root     string
}

type ExecRequest struct {
	ConfigPath string   `json:"config_path"`
	ContextID  string   `json:"context_id,omitempty"`
	Brief      string   `json:"brief,omitempty"`
	Files      []string `json:"files,omitempty"`
	Stdin      []byte   `json:"stdin,omitempty"`
	Actor      string   `json:"actor"`
}

func (c *Controller) Exec(ctx context.Context, request ExecRequest) (Execution, error) {
	if c.Engine == nil {
		return Execution{}, errors.New("Factory execution service is not configured")
	}
	id := uuid.NewString()
	config, err := factoryconfig.LoadExecution(request.ConfigPath, id)
	if err != nil {
		return Execution{}, err
	}
	if c.Adapters != nil {
		if err := c.Adapters.Validate(config.Configuration.Packages); err != nil {
			return Execution{}, err
		}
	}
	executionDirectory := filepath.Dir(config.Configuration.FilesDir)
	if err := os.MkdirAll(config.Configuration.FilesDir, 0700); err != nil {
		return Execution{}, fmt.Errorf("prepare execution files: %w", err)
	}
	if err := os.Chmod(executionDirectory, 0700); err != nil {
		return Execution{}, fmt.Errorf("protect execution directory: %w", err)
	}
	inputRoot := filepath.Join(executionDirectory, "inputs")
	bundle, err := inputs.Capture(inputRoot, request.Brief, request.Files, bytes.NewReader(request.Stdin))
	if err != nil {
		_ = os.RemoveAll(executionDirectory)
		return Execution{}, err
	}
	description := strings.TrimSpace(request.Brief)
	if index := strings.IndexByte(description, '\n'); index >= 0 {
		description = description[:index]
	}
	if len(description) > 100 {
		description = description[:100]
	}
	_, err = c.Store.Create(NewExecution{
		ID: id, ContextID: request.ContextID, Description: description,
		Workflow: config.Definition.Name, Project: config.Project, ConfigPath: config.Path,
		ConfigDigest: config.Digest, Brief: request.Brief, Input: bundle,
		Configuration: config.Configuration,
	})
	if err != nil {
		_ = os.RemoveAll(executionDirectory)
		return Execution{}, err
	}
	var artifacts []execution.Artifact
	for _, document := range bundle.Documents {
		artifacts = append(artifacts, execution.Artifact{
			Name: document.Name, URI: document.Path, Digest: document.Digest,
		})
	}
	started, err := c.Engine.Start(ctx, execution.StartRequest{
		ExecutionID: id, Project: config.Project, Brief: request.Brief,
		Inputs: artifacts, Configuration: config.Configuration,
		Actor: request.Actor, RequestID: uuid.NewString(),
	})
	if err != nil {
		return c.Store.Update(id, "execution_start_failed", err.Error(), "", func(current *Execution) error {
			current.State = execution.ExecutionFailed
			current.Error = err.Error()
			return nil
		})
	}
	return c.Store.Update(id, "execution_started", "Execution started", "", func(current *Execution) error {
		if current.State == execution.ExecutionReady {
			current.State = execution.ExecutionRunning
		}
		current.WorkflowRunID = started.RunID
		return nil
	})
}

func (c *Controller) Inbox(contextID string) ([]Request, error) {
	executions, err := c.Store.List(contextID)
	if err != nil {
		return nil, err
	}
	var requests []Request
	for _, record := range executions {
		if err = c.addInputRequests(&record); err != nil {
			return nil, err
		}
		for _, request := range record.Requests {
			if request.State == "pending" {
				requests = append(requests, request)
			}
		}
	}
	return requests, nil
}

func (c *Controller) Answer(ctx context.Context, requestID string, outcome execution.Outcome, reason, actor string) (Execution, error) {
	if c.Comms != nil {
		request, err := c.Comms.Input(ctx, requestID)
		if err != nil {
			return Execution{}, err
		}
		if request != nil {
			if actor == "" {
				return Execution{}, errors.New("answering actor is required")
			}
			answer := string(outcome)
			if len(request.Options) == 0 && reason != "" {
				answer = reason
			}
			if _, err = c.Comms.Answer(ctx, requestID, answer); err != nil {
				return Execution{}, err
			}
			return c.Store.Get(request.ExecutionID)
		}
	}

	kind, record, err := c.Store.Find(requestID)
	if err != nil {
		return Execution{}, err
	}
	if kind != "request" {
		return Execution{}, errors.New("answer requires a decision request ID")
	}
	var selected Request
	var step Step
	for _, candidate := range record.Requests {
		if candidate.ID == requestID {
			selected = candidate
			break
		}
	}
	if selected.State != "pending" {
		return Execution{}, fmt.Errorf("request %s is %s", requestID, selected.State)
	}
	if record.State != execution.ExecutionWaitingInput {
		return Execution{}, fmt.Errorf("execution %s is %s; decisions require a waiting execution", record.ID, record.State)
	}
	valid := false
	for _, option := range selected.Options {
		valid = valid || option == outcome
	}
	if !valid {
		return Execution{}, fmt.Errorf("outcome %q is not an option for request %s", outcome, requestID)
	}
	for _, candidate := range record.Steps {
		if candidate.ID == selected.StepID {
			step = candidate
			break
		}
	}
	if step.State != "waiting_input" {
		return Execution{}, fmt.Errorf("step %s is %s; decisions require a waiting step", step.ID, step.State)
	}
	if c.Engine == nil {
		return Execution{}, errors.New("Factory execution service is not configured")
	}
	if reason == "" {
		reason = string(outcome)
	}
	answeredAt := time.Now().UTC()
	if err := c.Engine.SendDecision(ctx, execution.Decision{
		ExecutionID: record.ID, Step: step.Key, Visit: step.Visit,
		Outcome: outcome, Actor: actor, Reason: reason, At: answeredAt,
	}); err != nil {
		return Execution{}, err
	}
	return c.Store.Update(record.ID, "decision_answered", "Decision answered: "+string(outcome), step.ID,
		func(current *Execution) error {
			if current.State != execution.ExecutionWaitingInput {
				return fmt.Errorf("execution %s changed to %s before its decision was recorded", current.ID, current.State)
			}
			for i := range current.Requests {
				if current.Requests[i].ID == requestID {
					if current.Requests[i].State != "pending" {
						return fmt.Errorf("request %s changed to %s before its decision was recorded", requestID, current.Requests[i].State)
					}
					current.Requests[i].State = "answered"
					current.Requests[i].Answer = outcome
					current.Requests[i].Reason = reason
					current.Requests[i].AnsweredAt = &answeredAt
				}
			}
			current.State = execution.ExecutionRunning
			return nil
		})
}

func (c *Controller) Cancel(ctx context.Context, executionID string) (Execution, error) {
	record, err := c.Store.Get(executionID)
	if err != nil {
		return Execution{}, err
	}
	if record.State == execution.ExecutionCompleted {
		return Execution{}, fmt.Errorf("execution %s is already %s", executionID, record.State)
	}
	if record.WorkflowRunID != "" && c.Engine == nil {
		return Execution{}, errors.New("Factory execution service is not configured")
	}
	// Persist the fence before cancelling the engine or stopping sessions: both
	// can synchronously produce late progress callbacks. Repeated cancellation
	// retries cleanup without appending another cancellation event.
	updated, err := c.Store.Apply(executionID, "execution_cancelled", "Execution cancelled", "", func(current *Execution) (bool, error) {
		if current.State == execution.ExecutionCompleted {
			return false, fmt.Errorf("execution %s is already completed", executionID)
		}
		if current.State == execution.ExecutionCancelled {
			return false, nil
		}
		current.State = execution.ExecutionCancelled
		for i := range current.Hooks {
			if current.Hooks[i].State != "succeeded" {
				current.Hooks[i].State = "cancelled"
			}
		}
		current.CurrentStepID = ""
		now := time.Now().UTC()
		finishLifecycle(current, "cancelled", now)
		for i := range current.Steps {
			step := &current.Steps[i]
			if step.State != "completed" && step.State != "failed" {
				step.State = "cancelled"
				step.FinishedAt = &now
			}
		}
		for i := range current.Requests {
			if current.Requests[i].State == "pending" {
				current.Requests[i].State = "cancelled"
			}
		}
		return true, nil
	})
	if err != nil {
		return Execution{}, err
	}
	var cleanupErrors []error
	if c.Comms != nil {
		cleanupErrors = append(cleanupErrors, c.Comms.FinishExecution(ctx, executionID))
	}
	if record.WorkflowRunID != "" {
		cleanupErrors = append(cleanupErrors, c.Engine.Cancel(ctx, record.WorkflowRunID))
	}
	for _, hook := range record.Hooks {
		if hook.SessionID != "" {
			session, err := c.Sessions.Get(hook.SessionID)
			if err == nil && session.State == sessions.Running {
				cleanupErrors = append(cleanupErrors, c.Sessions.Stop(hook.SessionID))
			}
		}
	}
	for _, step := range record.Steps {
		for _, id := range step.Sessions {
			session, err := c.Sessions.Get(id)
			if err == nil && session.State == sessions.Running {
				cleanupErrors = append(cleanupErrors, c.Sessions.Stop(id))
			}
		}
	}
	return updated, errors.Join(cleanupErrors...)
}

func (c *Controller) Retry(ctx context.Context, stepID, actor, reason string) (Execution, error) {
	kind, record, err := c.Store.Find(stepID)
	if err != nil {
		return Execution{}, err
	}
	if kind == "hook" {
		return c.retryHook(ctx, record, stepID, actor, reason)
	}
	if kind != "step" {
		return Execution{}, errors.New("retry requires a step or hook ID")
	}
	var step Step
	for _, candidate := range record.Steps {
		if candidate.ID == stepID {
			step = candidate
			break
		}
	}
	if step.State != "blocked" {
		return Execution{}, fmt.Errorf("step %s is %s; only a blocked step can be retried", stepID, step.State)
	}
	if c.Engine == nil {
		return Execution{}, errors.New("Factory execution service is not configured")
	}
	if reason == "" {
		reason = "operator retry"
	}
	_, err = c.Store.Apply(record.ID, "retry_requested", "Retry requested: "+reason, stepID,
		func(current *Execution) (bool, error) {
			for i := range current.Steps {
				if current.Steps[i].ID == stepID {
					if current.Steps[i].State != "blocked" {
						return false, fmt.Errorf("step %s is already %s", stepID, current.Steps[i].State)
					}
					current.Steps[i].State = "retrying"
					current.State = execution.ExecutionRunning
					return true, nil
				}
			}
			return false, os.ErrNotExist
		})
	if err != nil {
		return Execution{}, err
	}
	if err := c.Engine.SendRetry(ctx, execution.Retry{
		ExecutionID: record.ID, Step: step.Key, Visit: step.Visit, Try: step.Try,
		Actor: actor, Reason: reason,
	}); err != nil {
		_, _ = c.Store.Update(record.ID, "retry_delivery_failed", err.Error(), stepID, func(current *Execution) error {
			if current.State == execution.ExecutionCancelled {
				return errors.New("retry delivery ended after execution cancellation")
			}
			for i := range current.Steps {
				if current.Steps[i].ID == stepID {
					current.Steps[i].State = "blocked"
				}
			}
			current.State = execution.ExecutionBlocked
			return nil
		})
		return Execution{}, err
	}
	return c.Store.Get(record.ID)
}

func (c *Controller) addInputRequests(record *Execution) error {
	if c.Comms == nil {
		return nil
	}
	requests, err := c.Comms.Requests(context.Background(), record.ID)
	if err != nil {
		return err
	}
	for _, input := range requests {
		options := make([]execution.Outcome, len(input.Options))
		for i, option := range input.Options {
			options[i] = execution.Outcome(option)
		}
		for i := range record.Steps {
			if record.Steps[i].ID == input.StepID && record.Steps[i].State == "running" {
				record.Steps[i].State = "waiting_input"
			}
		}
		record.Requests = append(record.Requests, Request{ID: input.ID, ExecutionID: record.ID, Prompt: input.Prompt, Options: options, State: input.State, CreatedAt: input.At, Kind: "step_input", ThreadID: input.ThreadID, Owner: input.Owner, StepID: input.StepID})
	}
	return nil
}
