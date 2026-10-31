package hatchetengine

import (
	"controls/execution"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBudgetPolicySurvivesTaskSerialization(t *testing.T) {
	for _, action := range []string{"pause", "stop"} {
		raw, err := json.Marshal(execution.StepResult{Interruption: &execution.Interruption{Action: action, Reason: "Budget limit"}})
		require.NoError(t, err)
		var result execution.StepResult
		require.NoError(t, json.Unmarshal(raw, &result))
		step := execution.Step{Budget: &execution.Budget{Tokens: 1, Action: action}}
		require.Equal(t, action, budgetPolicy(validateResult(step, result)))
		step.Budget.Action = "warn"
		require.Equal(t, "", budgetPolicy(validateResult(step, result)))
	}
}
