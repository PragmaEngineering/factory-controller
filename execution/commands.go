package execution

import (
	"context"
	"encoding/json"
	"time"
)

// Command identifies a caller and an idempotent request. ExpectedVersion
// prevents a stale client from overwriting a newer execution transition.
type Command struct {
	ExecutionID     string `json:"execution_id"`
	Actor           string `json:"actor"`
	RequestID       string `json:"request_id"`
	ExpectedVersion uint64 `json:"expected_version"`
}

type StartRequest struct {
	ExecutionID   string        `json:"execution_id,omitempty"`
	Project       string        `json:"project"`
	Brief         string        `json:"brief"`
	Inputs        []Artifact    `json:"inputs,omitempty"`
	Configuration Configuration `json:"configuration"`
	RequestID     string        `json:"request_id"`
	Actor         string        `json:"actor"`
}

type SubmitRequest struct {
	Command
	AttemptID string     `json:"attempt_id"`
	Outcome   Outcome    `json:"outcome"`
	Summary   string     `json:"summary"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Evidence  []Evidence `json:"evidence,omitempty"`
}

type DecisionRequest struct {
	Command
	AttemptID string     `json:"attempt_id"`
	Outcome   Outcome    `json:"outcome"`
	Reason    string     `json:"reason"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

type RetryRequest struct {
	Command
	AttemptID string `json:"attempt_id"`
	Reason    string `json:"reason"`
}

type CancelRequest struct {
	Command
	Reason string `json:"reason"`
}

// ContinueRequest creates a configuration revision and starts a new visit to
// Step. Prior results remain in history; affected downstream work is superseded.
type ContinueRequest struct {
	Command
	Step          StepID        `json:"step"`
	Configuration Configuration `json:"configuration"`
	Reason        string        `json:"reason"`
}

type Attention struct {
	ID          string    `json:"id"`
	ExecutionID string    `json:"execution_id"`
	AttemptID   string    `json:"attempt_id"`
	Prompt      string    `json:"prompt"`
	Options     []Outcome `json:"options"`
	Authority   string    `json:"authority"`
}

// Event provides an ordered record for monitors and external agents. Data is
// event-specific and must exclude credentials and other secret values.
type Event struct {
	Sequence    uint64          `json:"sequence"`
	ExecutionID string          `json:"execution_id"`
	AttemptID   string          `json:"attempt_id,omitempty"`
	Kind        string          `json:"kind"`
	Actor       string          `json:"actor"`
	At          time.Time       `json:"at"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// Controller is the client-facing boundary. Implementations must authorize
// actors and apply each accepted transition atomically with its event record.
type Controller interface {
	StartExecution(context.Context, StartRequest) (Execution, error)
	GetExecution(context.Context, string) (Execution, error)
	SubmitResult(context.Context, SubmitRequest) (Execution, error)
	RecordDecision(context.Context, DecisionRequest) (Execution, error)
	RetryStep(context.Context, RetryRequest) (Execution, error)
	ContinueFrom(context.Context, ContinueRequest) (Execution, error)
	CancelExecution(context.Context, CancelRequest) (Execution, error)
	ListAttention(context.Context, string) ([]Attention, error)
	ListEvents(context.Context, string, uint64) ([]Event, error)
}
