// Package execution defines the durable contract between a workflow controller,
// its workers, and clients. It does not execute agents or enforce isolation.
package execution

import "encoding/json"

// StepID identifies a step within one workflow revision.
type StepID string

// Outcome is a worker result or decision chosen at a step. Workflows route
// outcomes explicitly; the controller must not infer a route from prose.
type Outcome string

// A route may target another step or one of these terminal destinations.
const (
	Complete StepID = "$complete"
	Fail     StepID = "$fail"
)

type StepKind string

const (
	// RunStep invokes an agent or deterministic package.
	RunStep StepKind = "run"
	// DecisionStep waits for an authorized external decision.
	DecisionStep StepKind = "decision"
)

// Workflow is the directed graph pinned to an execution. Step routes choose
// transitions; stages bound repeated routes and can require a human decision.
type Workflow struct {
	Name   string  `json:"name"`
	Start  StepID  `json:"start"`
	Stages []Stage `json:"stages,omitempty"`
	Steps  []Step  `json:"steps"`
}

// Stage groups executable steps and owns the policy for repeating them.
// Steps[0] is the entry point for each round. A route back to that entry
// starts another round; a human decision, when required, sits at the boundary.
type Stage struct {
	Hooks Hooks        `json:"hooks,omitempty"`
	ID    string       `json:"id"`
	Steps []StepID     `json:"steps"`
	Loop  *LoopPolicy  `json:"loop,omitempty"`
	Human *HumanPolicy `json:"human,omitempty"`
}

type LoopPolicy struct {
	Mode string `json:"mode,omitempty"`
	// Limit includes the initial round. Zero is invalid when a loop is set.
	Limit uint `json:"limit"`
}

type HumanPolicy struct {
	Required bool   `json:"required"`
	Step     StepID `json:"step,omitempty"`
}

// Configuration is an immutable, fully resolved execution definition. It
// contains no secret values; Settings may contain references to secrets.
type Configuration struct {
	Hooks    Hooks             `json:"hooks,omitempty"`
	Workflow Workflow          `json:"workflow"`
	Packages []PackageSnapshot `json:"packages"`
}

// PackageSnapshot records the package and effective settings selected for a
// run step. The runner interprets Settings; the controller pins its bytes.
type PackageSnapshot struct {
	Name     string          `json:"name"`
	Source   string          `json:"source"`
	Digest   string          `json:"digest"`
	Runner   string          `json:"runner"`
	Settings json.RawMessage `json:"settings,omitempty"`
	Access   AccessProfile   `json:"access"`
}

// AccessProfile is a requested grant, not proof of isolation. Empty lists
// grant nothing. A runner must enforce the grant before starting a package.
type AccessProfile struct {
	ReadPaths  []string `json:"read_paths,omitempty"`
	WritePaths []string `json:"write_paths,omitempty"`
	Network    []string `json:"network,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	MCPs       []string `json:"mcps,omitempty"`
	SecretRefs []string `json:"secret_refs,omitempty"`
}

type Step struct {
	InputBindings map[string]InputBinding `json:"input_bindings,omitempty"`
	Hooks         Hooks                   `json:"hooks,omitempty"`
	Budget        *Budget                 `json:"budget,omitempty"`
	ID            StepID                  `json:"id"`
	Stage         string                  `json:"stage,omitempty"`
	Title         string                  `json:"title,omitempty"`
	Instructions  string                  `json:"instructions,omitempty"`
	Kind          StepKind                `json:"kind"`
	Inputs        []string                `json:"inputs,omitempty"`
	Outputs       []string                `json:"outputs,omitempty"`
	OutputsOn     []Outcome               `json:"outputs_on,omitempty"`
	Run           *RunSpec                `json:"run,omitempty"`
	Decision      *DecisionSpec           `json:"decision,omitempty"`
	Retry         RetryPolicy             `json:"retry,omitempty"`
	Routes        map[Outcome]StepID      `json:"routes"`
}

// RunSpec names an independently runnable package in the same configuration.
// PackageSnapshot pins its bytes and settings for this revision.
type RunSpec struct {
	Package string `json:"package"`
}

// A decision step is reached through an explicit route. A preceding run step
// may classify whether the workflow should visit it at all.
type DecisionSpec struct {
	Authority string    `json:"authority"`
	Prompt    string    `json:"prompt"`
	Options   []Outcome `json:"options"`
	Artifacts []string  `json:"artifacts,omitempty"`
}

// RetryPolicy applies to one visit to a run step. MaxAttempts includes the
// first attempt. Zero means one attempt. Review loops are routes, not retries.
type RetryPolicy struct {
	MaxAttempts  uint          `json:"max_attempts,omitempty"`
	DelaySeconds uint          `json:"delay_seconds,omitempty"`
	Failures     []FailureKind `json:"failures,omitempty"`
	Outcomes     []Outcome     `json:"outcomes,omitempty"`
}

// RequiresOutputs applies the workflow's outcome-specific publication policy.
func (s Step) RequiresOutputs(outcome Outcome) bool {
	if len(s.OutputsOn) == 0 {
		return true
	}
	for _, selected := range s.OutputsOn {
		if selected == outcome {
			return true
		}
	}
	return false
}

func (s Stage) Concurrent() bool { return s.Loop != nil && s.Loop.Mode == "concurrent" }
