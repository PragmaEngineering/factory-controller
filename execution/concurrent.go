package execution

import "fmt"

// ConcurrentTarget reduces a round deterministically. Failure wins, then a
// repeat request, then the shared exit. No outcome spelling has special meaning.
func ConcurrentTarget(stage Stage, steps map[StepID]Step, results map[StepID]StepResult) (StepID, error) {
	for _, id := range stage.Steps {
		if _, ok := results[id]; !ok {
			return "", fmt.Errorf("concurrent round is missing step %s", id)
		}
	}
	entry := stage.Steps[0]
	repeat := false
	publication := results[stage.Steps[0]].PublicationID
	exit := StepID("")
	for _, id := range stage.Steps {
		result, ok := results[id]
		if !ok {
			return "", fmt.Errorf("concurrent round is missing step %s", id)
		}
		if result.PublicationID != publication {
			repeat = true
		}
		target, ok := steps[id].Routes[result.Outcome]
		if !ok {
			return "", fmt.Errorf("step %s has no route for %q", id, result.Outcome)
		}
		if target == Fail {
			return Fail, nil
		}
		if target == entry {
			repeat = true
			continue
		}
		if exit != "" && exit != target {
			return "", fmt.Errorf("concurrent stage %s has conflicting exits", stage.ID)
		}
		exit = target
	}
	if repeat {
		return entry, nil
	}
	if exit == "" {
		return "", fmt.Errorf("concurrent stage %s has no exit", stage.ID)
	}
	return exit, nil
}
