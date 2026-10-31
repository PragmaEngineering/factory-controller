package execution

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBindingsUseLatestPrecedingAcceptedVisit(t *testing.T) {
	work := Work{Brief: "Investigate the gateway", Inputs: []Artifact{{Name: "brief", URI: "/captured/brief", Digest: "abc"}}, Step: Step{InputBindings: map[string]InputBinding{"task": {Execution: "input"}, "request": {Step: "evaluate", Output: "request"}, "response": {Step: "evaluate", Output: "response"}}}, History: []CompletedStep{
		{Step: "evaluate", Visit: 1, Result: StepResult{Data: map[string]json.RawMessage{"request": json.RawMessage(`{"revision":1}`), "response": json.RawMessage(`{"choice":"accept"}`)}}},
		{Step: "evaluate", Visit: 2, Result: StepResult{Data: map[string]json.RawMessage{"request": json.RawMessage(`{"revision":2}`), "response": json.RawMessage(`{"choice":"clarify"}`)}}},
	}}
	bound, err := BindInputs(work)
	require.NoError(t, err)
	require.JSONEq(t, `{"revision":2}`, string(bound["request"]))
	require.JSONEq(t, `{"choice":"clarify"}`, string(bound["response"]))
	require.Contains(t, string(bound["task"]), "/captured/brief")
	delete(work.History[1].Result.Data, "response")
	_, err = BindInputs(work)
	require.ErrorContains(t, err, "visit 2 did not return")
	require.ErrorContains(t, err, "response")
	work.History = nil
	_, err = BindInputs(work)
	require.ErrorContains(t, err, "no preceding accepted result")
}

func TestBindingValidationRejectsAmbiguousSourcesAndConcurrentPeerDependencies(t *testing.T) {
	for _, binding := range []InputBinding{{}, {Execution: "other"}, {Execution: "input", Step: "producer", Output: "response"}, {Step: "producer"}, {Step: "missing", Output: "response"}, {Step: "consumer", Output: "response"}} {
		workflow := Workflow{Steps: []Step{{ID: "producer", Kind: RunStep}, {ID: "consumer", Kind: RunStep, InputBindings: map[string]InputBinding{"x": binding}}}}
		require.Error(t, validateBindings(workflow, map[StepID]Step{"producer": workflow.Steps[0], "consumer": workflow.Steps[1]}))
	}
	workflow := Workflow{Steps: []Step{{ID: "producer", Kind: RunStep}, {ID: "consumer", Kind: RunStep, InputBindings: map[string]InputBinding{"x": {Step: "producer", Output: "response"}}}}, Stages: []Stage{{ID: "pair", Steps: []StepID{"producer", "consumer"}, Loop: &LoopPolicy{Mode: "concurrent", Limit: 2}}}}
	steps := map[StepID]Step{"producer": workflow.Steps[0], "consumer": workflow.Steps[1]}
	require.ErrorContains(t, validateBindings(workflow, steps), "concurrent peer")
	workflow.Stages[0].Loop.Mode = "sequential"
	require.NoError(t, validateBindings(workflow, steps))
}
