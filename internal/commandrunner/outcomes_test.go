package commandrunner

import (
	"controls/execution"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOutputsOnRejectsUnknownOutcome(t *testing.T) {
	config := execution.Configuration{Workflow: execution.Workflow{
		Name: "fixture", Start: "work", Steps: []execution.Step{{ID: "work", Kind: execution.RunStep,
			Run: &execution.RunSpec{Package: "agent"}, Outputs: []string{"spec.html"}, OutputsOn: []execution.Outcome{"typo"},
			Routes: map[execution.Outcome]execution.StepID{"ready": execution.Complete},
		}},
	}, Packages: []execution.PackageSnapshot{{Name: "agent", Runner: "command", Digest: "fixture"}}}
	require.ErrorContains(t, execution.Validate(config), `outputs_on references unknown outcome "typo"`)
	config.Workflow.Steps[0].OutputsOn = []execution.Outcome{"ready"}
	require.NoError(t, execution.Validate(config))
	config.Workflow.Steps[0].Outputs = nil
	require.ErrorContains(t, execution.Validate(config), "outputs_on requires agent outputs")
}
