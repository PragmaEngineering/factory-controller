package execution

import "fmt"

// StageRouter applies stage loop limits to the ordinary step routes. The
// scheduler persists the workflow and replays this deterministic state from
// the sequence of accepted step results.
type StageRouter struct {
	stages map[string]Stage
	owners map[StepID]string
	rounds map[string]uint
}

func NewStageRouter(workflow Workflow) *StageRouter {
	router := &StageRouter{
		stages: make(map[string]Stage, len(workflow.Stages)),
		owners: make(map[StepID]string),
		rounds: make(map[string]uint),
	}
	for _, stage := range workflow.Stages {
		router.stages[stage.ID] = stage
		for _, step := range stage.Steps {
			router.owners[step] = stage.ID
		}
		if stage.Human != nil && stage.Human.Required {
			router.owners[stage.Human.Step] = stage.ID
		}
		if workflow.Start == stage.Steps[0] {
			router.rounds[stage.ID] = 1
		}
	}
	return router
}

// Next returns the configured target, except when another round would exceed
// a stage's limit. A required human decision receives that exception; without
// one, the stage stops. The bool reports a limit escalation for observability.
func (r *StageRouter) Next(current, target StepID) (StepID, bool, error) {
	currentStage := r.owners[current]
	if currentStage != "" {
		stage := r.stages[currentStage]
		if stage.Human != nil && stage.Human.Required && current == stage.Human.Step {
			delete(r.rounds, currentStage)
		}
	}
	targetStage := r.owners[target]
	if targetStage == "" {
		return target, false, nil
	}
	stage := r.stages[targetStage]
	if target != stage.Steps[0] {
		return target, false, nil
	}
	if currentStage != targetStage || (stage.Human != nil && current == stage.Human.Step) {
		r.rounds[targetStage] = 1
		return target, false, nil
	}
	if stage.Loop == nil {
		return target, false, fmt.Errorf("stage %q has an unbounded back route", stage.ID)
	}
	if r.rounds[targetStage] >= stage.Loop.Limit {
		if stage.Human == nil || !stage.Human.Required {
			return "", false, fmt.Errorf("stage %q reached loop limit %d without a human decision", stage.ID, stage.Loop.Limit)
		}
		return stage.Human.Step, true, nil
	}
	r.rounds[targetStage]++
	return target, false, nil
}

func (r *StageRouter) Limit(stageID string) uint {
	if stage, exists := r.stages[stageID]; exists && stage.Loop != nil {
		return stage.Loop.Limit
	}
	return 0
}
