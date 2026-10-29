package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStageRouterEscalatesOnlyAfterConfiguredRounds(t *testing.T) {
	workflow := Workflow{Start: "build", Stages: []Stage{{
		ID: "implementation", Steps: []StepID{"build", "review"},
		Loop: &LoopPolicy{Limit: 3}, Human: &HumanPolicy{Required: true, Step: "acceptance"},
	}}}
	router := NewStageRouter(workflow)
	for round := 1; round <= 2; round++ {
		next, escalated, err := router.Next("build", "review")
		require.NoError(t, err)
		require.Equal(t, StepID("review"), next)
		require.False(t, escalated)
		next, escalated, err = router.Next("review", "build")
		require.NoError(t, err)
		require.Equal(t, StepID("build"), next)
		require.False(t, escalated)
	}
	next, escalated, err := router.Next("review", "build")
	require.NoError(t, err)
	require.Equal(t, StepID("acceptance"), next)
	require.True(t, escalated)

	// A person's request for changes starts a fresh bounded series.
	next, escalated, err = router.Next("acceptance", "build")
	require.NoError(t, err)
	require.Equal(t, StepID("build"), next)
	require.False(t, escalated)
	next, escalated, err = router.Next("review", "build")
	require.NoError(t, err)
	require.Equal(t, StepID("build"), next)
	require.False(t, escalated)
}

func TestStageRouterAllowsEarlyApprovalAndRejectsUnreviewedLimit(t *testing.T) {
	t.Run("early approval", func(t *testing.T) {
		router := NewStageRouter(Workflow{Start: "build", Stages: []Stage{{
			ID: "implementation", Steps: []StepID{"build", "review"},
			Loop: &LoopPolicy{Limit: 3}, Human: &HumanPolicy{Required: true, Step: "acceptance"},
		}}})
		next, escalated, err := router.Next("review", "acceptance")
		require.NoError(t, err)
		require.Equal(t, StepID("acceptance"), next)
		require.False(t, escalated)
	})
	t.Run("without human", func(t *testing.T) {
		router := NewStageRouter(Workflow{Start: "build", Stages: []Stage{{
			ID: "implementation", Steps: []StepID{"build", "review"}, Loop: &LoopPolicy{Limit: 1},
		}}})
		_, _, err := router.Next("review", "build")
		require.ErrorContains(t, err, "reached loop limit 1 without a human decision")
	})
}
