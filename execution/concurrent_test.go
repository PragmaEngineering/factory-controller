package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrentRoundRouting(t *testing.T) {
	stage := Stage{ID: "pair", Steps: []StepID{"a", "b"}, Loop: &LoopPolicy{Mode: "concurrent", Limit: 3}}
	steps := map[StepID]Step{
		"a": {ID: "a", Routes: map[Outcome]StepID{"finished": "acceptance", "again": "a", "unavailable": Fail}},
		"b": {ID: "b", Routes: map[Outcome]StepID{"finished": "acceptance", "again": "a", "unavailable": Fail}},
	}
	for _, tc := range []struct {
		name string
		a, b Outcome
		want StepID
	}{{"both done", "finished", "finished", "acceptance"}, {"repeat requested", "finished", "again", "a"}, {"failure overrides repeat", "again", "unavailable", Fail}} {
		t.Run(tc.name, func(t *testing.T) {
			target, err := ConcurrentTarget(stage, steps, map[StepID]StepResult{"a": {Outcome: tc.a}, "b": {Outcome: tc.b}})
			require.NoError(t, err)
			require.Equal(t, tc.want, target)
		})
	}
	_, err := ConcurrentTarget(stage, steps, map[StepID]StepResult{"a": {Outcome: "unavailable"}})
	require.ErrorContains(t, err, "missing step")
	target, err := ConcurrentTarget(stage, steps, map[StepID]StepResult{"a": {Outcome: "finished", PublicationID: "old"}, "b": {Outcome: "finished", PublicationID: "new"}})
	require.NoError(t, err)
	require.Equal(t, StepID("a"), target)

}
