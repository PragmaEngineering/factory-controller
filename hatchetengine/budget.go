package hatchetengine

import (
	"controls/execution"
	"errors"
)

// Budget errors are reconstructed after task result deserialization. They
// bypass automatic retry so a retry policy cannot silently reset a limit.
type budgetError struct{ action, reason string }

func (e *budgetError) Error() string { return e.reason }
func budgetPolicy(err error) string {
	var budget *budgetError
	if errors.As(err, &budget) {
		return budget.action
	}
	return ""
}
func budgetResult(step execution.Step, result execution.StepResult) error {
	if result.Interruption == nil {
		return nil
	}
	v := result.Interruption
	if step.Budget == nil || v.Action != step.Budget.Action || (v.Action != "pause" && v.Action != "stop") || v.Reason == "" {
		return errors.New("invalid host budget interruption")
	}
	return &budgetError{action: v.Action, reason: v.Reason}
}
