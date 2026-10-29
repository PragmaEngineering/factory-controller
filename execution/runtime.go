package execution

import (
	"context"
	"time"
)

// Engine is the workflow scheduler port used by the control plane. A concrete
// scheduler owns execution and replay; Factory owns authorization and records.
type Engine interface {
	Start(context.Context, StartRequest) (Started, error)
	SendDecision(context.Context, Decision) error
	SendRetry(context.Context, Retry) error
	Cancel(context.Context, string) error
}

// Runner executes a pinned package without knowing which workflow scheduler
// invoked it. An agent is one possible package implementation.
type Runner interface {
	Run(context.Context, Work) (StepResult, error)
}

type Observer interface {
	Record(context.Context, Progress) error
}

type Work struct {
	ExecutionID string          `json:"execution_id"`
	Project     string          `json:"project"`
	Brief       string          `json:"brief"`
	Inputs      []Artifact      `json:"inputs,omitempty"`
	Step        Step            `json:"step"`
	Package     PackageSnapshot `json:"package"`
	Visit       uint            `json:"visit"`
	Try         uint            `json:"try"`
	History     []CompletedStep `json:"history,omitempty"`
	Peers       []Peer          `json:"peers,omitempty"`
}

type CompletedStep struct {
	Step   StepID     `json:"step"`
	Visit  uint       `json:"visit"`
	Try    uint       `json:"try"`
	Result StepResult `json:"result"`
}

type Progress struct {
	Kind        string          `json:"kind"`
	ExecutionID string          `json:"execution_id"`
	Step        Step            `json:"step"`
	Visit       uint            `json:"visit"`
	Try         uint            `json:"try"`
	Result      *StepResult     `json:"result,omitempty"`
	Message     string          `json:"message,omitempty"`
	Error       string          `json:"error,omitempty"`
	History     []CompletedStep `json:"history,omitempty"`
}

type Started struct {
	ExecutionID string `json:"execution_id"`
	RunID       string `json:"run_id"`
}

type Decision struct {
	ExecutionID string    `json:"execution_id"`
	Step        StepID    `json:"step"`
	Visit       uint      `json:"visit"`
	Outcome     Outcome   `json:"outcome"`
	Actor       string    `json:"actor"`
	Reason      string    `json:"reason"`
	At          time.Time `json:"at"`
}

type Retry struct {
	ExecutionID string `json:"execution_id"`
	Step        StepID `json:"step"`
	Visit       uint   `json:"visit"`
	Try         uint   `json:"try"`
	Actor       string `json:"actor"`
	Reason      string `json:"reason"`
}

// Peer names another invocation in the same concurrently dispatched round.
type Peer struct {
	Step  StepID `json:"step"`
	Name  string `json:"name"`
	Visit uint   `json:"visit"`
	Try   uint   `json:"try"`
}
