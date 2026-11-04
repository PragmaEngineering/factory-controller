package execution

import (
	"fmt"
)

// Validate rejects configurations whose routes or package references cannot
// execute. It runs before an execution is started.
func Validate(config Configuration) error {
	if err := config.Hooks.Validate(); err != nil {
		return fmt.Errorf("execution hooks: %w", err)
	}
	workflow := config.Workflow
	if workflow.Name == "" || workflow.Start == "" {
		return fmt.Errorf("workflow name and start step are required")
	}
	steps := make(map[StepID]Step, len(workflow.Steps))
	for _, step := range workflow.Steps {
		if err := step.Hooks.Validate(); err != nil {
			return fmt.Errorf("step %q hooks: %w", step.ID, err)
		}
		if err := step.Budget.Validate(); err != nil {
			return fmt.Errorf("step %q: %w", step.ID, err)
		}
		if step.Budget != nil && step.Kind != RunStep {
			return fmt.Errorf("step %q: budgets require a run step", step.ID)
		}
		if step.ID == "" || step.ID == Complete || step.ID == Fail {
			return fmt.Errorf("invalid step ID %q", step.ID)
		}
		if _, exists := steps[step.ID]; exists {
			return fmt.Errorf("duplicate step %q", step.ID)
		}
		steps[step.ID] = step
	}
	if _, exists := steps[workflow.Start]; !exists {
		return fmt.Errorf("start step %q does not exist", workflow.Start)
	}
	packages := make(map[string]struct{}, len(config.Packages))
	for _, pkg := range config.Packages {
		if err := pkg.Runtime.Validate(); err != nil {
			return fmt.Errorf("package %q: %w", pkg.Name, err)
		}
		if pkg.Name == "" || pkg.Digest == "" || pkg.Runner == "" {
			return fmt.Errorf("package %q requires name, digest, and runner", pkg.Name)
		}
		if _, exists := packages[pkg.Name]; exists {
			return fmt.Errorf("duplicate package %q", pkg.Name)
		}
		packages[pkg.Name] = struct{}{}
	}
	for _, step := range workflow.Steps {
		if len(step.Routes) == 0 {
			return fmt.Errorf("step %q has no routes", step.ID)
		}
		for outcome, target := range step.Routes {
			if outcome == "" {
				return fmt.Errorf("step %q has an empty outcome", step.ID)
			}
			if target != Complete && target != Fail {
				if _, exists := steps[target]; !exists {
					return fmt.Errorf("step %q routes to unknown step %q", step.ID, target)
				}
			}
		}
		if len(step.OutputsOn) > 0 && (len(step.Outputs) == 0 || step.Kind != RunStep) {
			return fmt.Errorf("step %q: outputs_on requires agent outputs", step.ID)
		}
		for _, outcome := range step.OutputsOn {
			if _, exists := step.Routes[outcome]; !exists {
				return fmt.Errorf("step %q: outputs_on references unknown outcome %q", step.ID, outcome)
			}
		}
		switch step.Kind {
		case RunStep:
			if step.Run == nil || step.Decision != nil {
				return fmt.Errorf("run step %q requires only a run specification", step.ID)
			}
			if _, exists := packages[step.Run.Package]; !exists {
				return fmt.Errorf("run step %q references unknown package %q", step.ID, step.Run.Package)
			}
		case DecisionStep:
			if step.Decision == nil || step.Run != nil {
				return fmt.Errorf("decision step %q requires only a decision specification", step.ID)
			}
			if step.Decision.Authority == "" || len(step.Decision.Options) == 0 {
				return fmt.Errorf("decision step %q requires authority and options", step.ID)
			}
			for _, option := range step.Decision.Options {
				if _, exists := step.Routes[option]; !exists {
					return fmt.Errorf("decision step %q has no route for option %q", step.ID, option)
				}
			}
		default:
			return fmt.Errorf("step %q has unknown kind %q", step.ID, step.Kind)
		}
	}
	for _, stage := range workflow.Stages {
		if !stage.Concurrent() || len(stage.Steps) == 0 {
			continue
		}
		for _, member := range stage.Steps[1:] {
			if workflow.Start == member {
				return fmt.Errorf("concurrent loop must start at its first step")
			}
			for _, step := range workflow.Steps {
				for _, target := range step.Routes {
					if target == member {
						return fmt.Errorf("concurrent loop must be entered through %q", stage.Steps[0])
					}
				}
			}
		}
	}
	if err := validateBindings(workflow, steps); err != nil {
		return err
	}
	if err := validateStages(workflow.Stages, steps); err != nil {
		return err
	}
	return nil
}

func validateStages(stages []Stage, steps map[StepID]Step) error {
	stageIDs := make(map[string]struct{}, len(stages))
	owners := make(map[StepID]string)
	for _, stage := range stages {
		if err := stage.Hooks.Validate(); err != nil {
			return fmt.Errorf("stage %q hooks: %w", stage.ID, err)
		}
		if stage.ID == "" || len(stage.Steps) == 0 {
			return fmt.Errorf("stage requires an ID and at least one step")
		}
		if _, exists := stageIDs[stage.ID]; exists {
			return fmt.Errorf("duplicate stage %q", stage.ID)
		}
		stageIDs[stage.ID] = struct{}{}
		if stage.Loop != nil && stage.Loop.Limit == 0 {
			return fmt.Errorf("stage %q loop limit must be positive", stage.ID)
		}
		if stage.Loop != nil && stage.Loop.Mode != "" && stage.Loop.Mode != "sequential" && stage.Loop.Mode != "concurrent" {
			return fmt.Errorf("stage %q: loop mode must be sequential or concurrent", stage.ID)
		}
		positions := make(map[StepID]int, len(stage.Steps))
		for index, stepID := range stage.Steps {
			step, exists := steps[stepID]
			if !exists {
				return fmt.Errorf("stage %q references unknown step %q", stage.ID, stepID)
			}
			if step.Kind != RunStep {
				return fmt.Errorf("stage %q body step %q must be a run step", stage.ID, stepID)
			}
			if owner, exists := owners[stepID]; exists {
				return fmt.Errorf("step %q belongs to both %q and %q", stepID, owner, stage.ID)
			}
			owners[stepID] = stage.ID
			positions[stepID] = index
			if step.Stage != stage.ID {
				return fmt.Errorf("step %q has stage %q, expected %q", stepID, step.Stage, stage.ID)
			}
		}
		humanStep := StepID("")
		if stage.Human != nil {
			if !stage.Human.Required && stage.Human.Step != "" {
				return fmt.Errorf("stage %q names a human step without requiring approval", stage.ID)
			}
			if stage.Human.Required {
				humanStep = stage.Human.Step
				human, exists := steps[humanStep]
				if !exists || human.Kind != DecisionStep {
					return fmt.Errorf("stage %q requires an existing human decision step", stage.ID)
				}
				if human.Decision == nil || human.Decision.Authority != "human" {
					return fmt.Errorf("stage %q human decision step %q must have human authority", stage.ID, humanStep)
				}
				if owner, exists := owners[humanStep]; exists {
					return fmt.Errorf("human step %q belongs to both %q and %q", humanStep, owner, stage.ID)
				}
				owners[humanStep] = stage.ID
				if human.Stage != stage.ID {
					return fmt.Errorf("human step %q has stage %q, expected %q", humanStep, human.Stage, stage.ID)
				}
			}
		}
		exit := StepID("")
		for _, stepID := range stage.Steps {
			for _, target := range steps[stepID].Routes {
				if stage.Concurrent() {
					if _, inside := positions[target]; inside && target != stage.Steps[0] {
						return fmt.Errorf("concurrent stage %q: internal routes must target loop entry %q", stage.ID, stage.Steps[0])
					}
					if _, inside := positions[target]; !inside && target != Fail {
						if exit != "" && exit != target {
							return fmt.Errorf("concurrent stage %q has conflicting exit targets", stage.ID)
						}
						exit = target
					}
				}
			}
		}
		for _, stepID := range stage.Steps {
			for _, target := range steps[stepID].Routes {
				if index, inside := positions[target]; inside {
					if index <= positions[stepID] && target != stage.Steps[0] {
						return fmt.Errorf("stage %q may loop only to its first step %q", stage.ID, stage.Steps[0])
					}
					if index <= positions[stepID] && stage.Loop == nil {
						return fmt.Errorf("stage %q has a back route but no loop limit", stage.ID)
					}
					continue
				}
				if humanStep != "" && target != humanStep && target != Fail {
					return fmt.Errorf("stage %q requires human step %q before proceeding from %q", stage.ID, humanStep, stepID)
				}
			}
		}
	}
	return nil
}
