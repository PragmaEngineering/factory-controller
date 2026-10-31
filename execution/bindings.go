package execution

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InputBinding selects either the original execution input or one named JSON
// output from a preceding accepted step result. It never evaluates expressions.
type InputBinding struct {
	Execution string `json:"execution,omitempty"`
	Step      StepID `json:"step,omitempty"`
	Output    string `json:"output,omitempty"`
}

func validateBindings(workflow Workflow, steps map[StepID]Step) error {
	for _, consumer := range workflow.Steps {
		if len(consumer.InputBindings) > 0 && consumer.Kind != RunStep {
			return fmt.Errorf("step %q: input_bindings require a run step", consumer.ID)
		}
		for name, binding := range consumer.InputBindings {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("step %q has an empty binding name", consumer.ID)
			}
			if binding.Execution != "" {
				if binding.Execution != "input" || binding.Step != "" || binding.Output != "" {
					return fmt.Errorf("step %q binding %q: execution must select only input", consumer.ID, name)
				}
				continue
			}
			producer, ok := steps[binding.Step]
			if !ok || producer.Kind != RunStep || binding.Output == "" || binding.Step == consumer.ID {
				return fmt.Errorf("step %q binding %q requires another run step and a named output", consumer.ID, name)
			}
			for _, stage := range workflow.Stages {
				if !stage.Concurrent() {
					continue
				}
				producerMember, consumerMember := false, false
				for _, member := range stage.Steps {
					producerMember = producerMember || member == producer.ID
					consumerMember = consumerMember || member == consumer.ID
				}
				if producerMember && consumerMember {
					return fmt.Errorf("step %q cannot bind outputs from concurrent peer %q; evaluate after the group finishes", consumer.ID, producer.ID)
				}
			}
		}
	}
	return nil
}

// BindInputs uses only immutable assignment metadata and accepted execution
// history. The history's last matching entry is the latest preceding visit;
// an uncompleted concurrent peer or another execution is never consulted.
func BindInputs(work Work) (map[string]json.RawMessage, error) {
	bound := make(map[string]json.RawMessage, len(work.Step.InputBindings))
	for name, binding := range work.Step.InputBindings {
		if binding.Execution == "input" {
			raw, err := json.Marshal(struct {
				Brief     string     `json:"brief"`
				Documents []Artifact `json:"documents"`
			}{work.Brief, work.Inputs})
			if err != nil {
				return nil, err
			}
			bound[name] = raw
			continue
		}
		found := false
		for i := len(work.History) - 1; i >= 0; i-- {
			previous := work.History[i]
			if previous.Step != binding.Step {
				continue
			}
			raw, ok := previous.Result.Data[binding.Output]
			if !ok {
				return nil, fmt.Errorf("binding %q: step %s visit %d did not return named output %q", name, binding.Step, previous.Visit, binding.Output)
			}
			if !json.Valid(raw) {
				return nil, fmt.Errorf("binding %q: output is invalid JSON", name)
			}
			bound[name] = append(json.RawMessage(nil), raw...)
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("binding %q: step %s has no preceding accepted result", name, binding.Step)
		}
	}
	return bound, nil
}
