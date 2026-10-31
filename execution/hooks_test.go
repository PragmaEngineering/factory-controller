package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHooksValidateAndIdentifyBoundaries(t *testing.T) {
	valid := Hook{Name: "prepare", Command: "go", Arguments: []string{"run", "./utility", "argument with spaces"}}
	require.NoError(t, (Hooks{Before: []Hook{valid}, After: []Hook{valid}}).Validate())
	require.Error(t, (Hooks{Before: []Hook{valid, valid}}).Validate())
	require.Error(t, (Hooks{Before: []Hook{{Name: "empty"}}}).Validate())
	scope := HookContext{ExecutionID: "execution", Scope: "step", Boundary: "after", Name: "commit", Step: "build", StepVisit: 2, StepAttempt: 1}
	first := scope.ID()
	require.Equal(t, first, scope.ID())
	scope.StepAttempt++
	require.NotEqual(t, first, scope.ID())
}
