package hatchetengine

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"controls/execution"

	hatchetclient "github.com/hatchet-dev/hatchet/pkg/client"
	hatchet "github.com/hatchet-dev/hatchet/sdks/go"
)

func TestValidateWorkflowReferences(t *testing.T) {
	config := execution.Configuration{
		Workflow: execution.Workflow{
			Name:  "coding",
			Start: "build",
			Steps: []execution.Step{
				{
					ID: "build", Kind: execution.RunStep,
					Run:    &execution.RunSpec{Package: "coder"},
					Routes: map[execution.Outcome]execution.StepID{"ready": "approve"},
				},
				{
					ID: "approve", Kind: execution.DecisionStep,
					Decision: &execution.DecisionSpec{Authority: "human", Options: []execution.Outcome{"yes"}},
					Routes:   map[execution.Outcome]execution.StepID{"yes": execution.Complete},
				},
			},
		},
		Packages: []execution.PackageSnapshot{{Name: "coder", Digest: "sha256:abc", Runner: "pi"}},
	}
	if err := Validate(config); err != nil {
		t.Fatalf("valid workflow rejected: %v", err)
	}

	config.Workflow.Steps[0].Routes["ready"] = "missing"
	if err := Validate(config); err == nil {
		t.Fatal("route to an unknown step was accepted")
	}
	config.Workflow.Steps[0].Routes["ready"] = "approve"
	config.Packages = nil
	if err := Validate(config); err == nil {
		t.Fatal("missing package was accepted")
	}
}

func TestReplayErrorsAreNotAgentFailures(t *testing.T) {
	for _, err := range []error{
		&hatchetclient.NonDeterminismError{},
		&hatchet.NonDeterminismError{},
		fmt.Errorf("wrapped replay error: %w", &hatchetclient.NonDeterminismError{}),
	} {
		if !isDurableReplayError(err) {
			t.Fatalf("replay error misclassified as an agent failure: %T", err)
		}
	}
	if isDurableReplayError(errors.New("agent exited 1")) {
		t.Fatal("agent failure classified as a replay error")
	}
}

func TestRetryScopeIncludesAttempt(t *testing.T) {
	first := retryScope("execution", "build", 1, 1)
	second := retryScope("execution", "build", 1, 2)
	if first == second || first == decisionScope("execution", "build", 1) {
		t.Fatal("retry events from different attempts share a scope")
	}
}

func TestValidateResultRequiresPublishedOutput(t *testing.T) {
	step := execution.Step{
		ID: "build", Outputs: []string{"source"},
		Routes: map[execution.Outcome]execution.StepID{"ready": execution.Complete},
	}
	result := execution.StepResult{Outcome: "ready", Summary: "source ready", Actor: "coder", At: time.Now()}
	if err := validateResult(step, result); err == nil {
		t.Fatal("result without its declared output was accepted")
	}
	result.Artifacts = []execution.Artifact{{Name: "source", URI: "artifact://source", Digest: "sha256:abc"}}
	if err := validateResult(step, result); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
}
