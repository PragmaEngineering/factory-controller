package execution

import (
	"encoding/json"
	"time"
)

type ExecutionState string

const (
	ExecutionReady        ExecutionState = "ready"
	ExecutionRunning      ExecutionState = "running"
	ExecutionWaitingInput ExecutionState = "waiting_input"
	ExecutionBlocked      ExecutionState = "blocked"
	ExecutionCompleted    ExecutionState = "completed"
	ExecutionFailed       ExecutionState = "failed"
	ExecutionCancelled    ExecutionState = "cancelled"
)

type AttemptState string

const (
	AttemptReady       AttemptState = "ready"
	AttemptRunning     AttemptState = "running"
	AttemptWaiting     AttemptState = "waiting_input"
	AttemptSubmitted   AttemptState = "submitted"
	AttemptInterrupted AttemptState = "interrupted"
)

// Execution is the durable view returned to clients. Version is increased by
// each accepted state change and used to reject stale commands.
type Execution struct {
	ID             string           `json:"id"`
	Project        string           `json:"project"`
	Brief          string           `json:"brief"`
	State          ExecutionState   `json:"state"`
	Version        uint64           `json:"version"`
	Configuration  uint64           `json:"configuration"`
	CurrentAttempt string           `json:"current_attempt,omitempty"`
	Configurations []ConfigRevision `json:"configurations"`
	Attempts       []StepAttempt    `json:"attempts"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// ConfigRevision pins the workflow and package configuration used by attempts
// referencing its number. Digest covers the resolved, non-secret configuration;
// secret values are supplied to the runner separately.
type ConfigRevision struct {
	Number        uint64        `json:"number"`
	Digest        string        `json:"digest"`
	Configuration Configuration `json:"configuration"`
	CreatedAt     time.Time     `json:"created_at"`
}

// Visit increases whenever routing enters a step, including a review loop.
// Try increases only for an automatic retry within that visit.
type StepAttempt struct {
	ID            string       `json:"id"`
	Step          StepID       `json:"step"`
	Visit         uint         `json:"visit"`
	Try           uint         `json:"try"`
	Configuration uint64       `json:"configuration"`
	State         AttemptState `json:"state"`
	Worker        string       `json:"worker,omitempty"`
	Result        *StepResult  `json:"result,omitempty"`
	Failure       *StepFailure `json:"failure,omitempty"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	FinishedAt    *time.Time   `json:"finished_at,omitempty"`
	SupersededBy  string       `json:"superseded_by,omitempty"`
}

// A submitted result ends an attempt, even when its outcome routes back to an
// earlier step. Submission is not task completion or human approval.
type StepResult struct {
	Data          map[string]json.RawMessage `json:"data,omitempty"`
	Interruption  *Interruption              `json:"interruption,omitempty"`
	PublicationID string                     `json:"publication_id,omitempty"`
	Outcome       Outcome                    `json:"outcome"`
	Summary       string                     `json:"summary"`
	Artifacts     []Artifact                 `json:"artifacts,omitempty"`
	Evidence      []Evidence                 `json:"evidence,omitempty"`
	Actor         string                     `json:"actor"`
	At            time.Time                  `json:"at"`
}

type Artifact struct {
	Name   string `json:"name"`
	URI    string `json:"uri"`
	Digest string `json:"digest"`
}

type Evidence struct {
	Kind string `json:"kind"`
	URI  string `json:"uri"`
}

type FailureKind string

const (
	FailureStart      FailureKind = "start_error"
	FailureExit       FailureKind = "worker_exit"
	FailureTimeout    FailureKind = "timeout"
	FailureLostWorker FailureKind = "lost_worker"
)

// StepFailure records an attempt that ended without an accepted result.
type StepFailure struct {
	Kind    FailureKind `json:"kind"`
	Message string      `json:"message"`
	At      time.Time   `json:"at"`
}
