package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Hooks surround the resource containing them. After runs on successful exit,
// not as unconditional cleanup. Lists execute in declaration order.
type Hooks struct {
	Before []Hook `json:"before,omitempty"`
	After  []Hook `json:"after,omitempty"`
}
type Hook struct {
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments,omitempty"`
}

func (h Hooks) Validate() error {
	for _, list := range [][]Hook{h.Before, h.After} {
		names := map[string]bool{}
		for _, hook := range list {
			if strings.TrimSpace(hook.Name) == "" || strings.ContainsAny(hook.Name, "\x00\r\n") {
				return fmt.Errorf("hook name is required and must be one line")
			}
			if names[hook.Name] {
				return fmt.Errorf("duplicate hook %q in one boundary", hook.Name)
			}
			names[hook.Name] = true
			if strings.TrimSpace(hook.Command) == "" || strings.ContainsRune(hook.Command, 0) {
				return fmt.Errorf("hook %q requires an executable command", hook.Name)
			}
			for _, arg := range hook.Arguments {
				if strings.ContainsRune(arg, 0) {
					return fmt.Errorf("hook %q has a NUL argument", hook.Name)
				}
			}
		}
	}
	return nil
}

// HookContext is the versioned JSON stdin contract. InvocationID is unchanged
// when retrying the hook; step attempts and stage entries have distinct IDs.
type HookContext struct {
	FilesDir     string            `json:"files_dir,omitempty"`
	Variables    map[string]string `json:"variables,omitempty"`
	Version      int               `json:"version"`
	InvocationID string            `json:"invocation_id"`
	ExecutionID  string            `json:"execution_id"`
	Project      string            `json:"project"`
	Scope        string            `json:"scope"`
	Boundary     string            `json:"boundary"`
	Name         string            `json:"name"`
	Stage        string            `json:"stage,omitempty"`
	StageVisit   uint              `json:"stage_visit,omitempty"`
	Step         StepID            `json:"step,omitempty"`
	StepVisit    uint              `json:"step_visit,omitempty"`
	StepAttempt  uint              `json:"step_attempt,omitempty"`
	Outcome      Outcome           `json:"outcome,omitempty"`
	Participants []HookParticipant `json:"participants,omitempty"`
}
type HookParticipant struct {
	Agent      string  `json:"agent"`
	InstanceID string  `json:"instance_id"`
	Step       StepID  `json:"step"`
	Visit      uint    `json:"visit"`
	Attempt    uint    `json:"attempt"`
	Outcome    Outcome `json:"outcome"`
}

func (c HookContext) ID() string {
	identity, _ := json.Marshal([]any{"controls/hook/v1", c.ExecutionID, c.Scope, c.Boundary, c.Stage, c.StageVisit, c.Step, c.StepVisit, c.StepAttempt, c.Name})
	return uuid.NewSHA1(uuid.NameSpaceOID, identity).String()
}

type HookWork struct {
	Hook    Hook        `json:"hook"`
	Context HookContext `json:"context"`
	Try     uint        `json:"try"`
}

// HookRunner is a host utility port, independent of the scheduler and agents.
type HookRunner interface {
	RunHook(context.Context, HookWork) (HookRun, error)
}
type HookRun struct {
	ID         string      `json:"id"`
	Context    HookContext `json:"context"`
	Try        uint        `json:"try"`
	State      string      `json:"state"`
	SessionID  string      `json:"session_id,omitempty"`
	Error      string      `json:"error,omitempty"`
	StartedAt  *time.Time  `json:"started_at,omitempty"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
}
