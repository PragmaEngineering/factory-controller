package hatchetengine

import (
	"controls/execution"
	"fmt"
)

// Workflow rules are scheduler-neutral. This adapter additionally rejects a
// group that can never fit its configured local worker capacity.
func Validate(config execution.Configuration) error {
	if err := execution.Validate(config); err != nil {
		return err
	}
	for _, stage := range config.Workflow.Stages {
		if stage.Concurrent() && len(stage.Steps) > PackageSlots {
			return fmt.Errorf("concurrent loop %q needs %d slots; the local worker provides %d", stage.ID, len(stage.Steps), PackageSlots)
		}
	}
	return nil
}
