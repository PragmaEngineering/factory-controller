package sessions

import (
	"fmt"
)

// Retries of one visit retain their native contract. A new visit/execution may
// choose a new contract; silently upgrading an existing retry is forbidden.
func validateRetryRuntime(previous []Session, request Launch) error {
	if request.Runtime == nil || request.StepID == "" || request.Try < 2 {
		return nil
	}
	for _, prior := range previous {
		if prior.StepID != request.StepID || prior.Try >= request.Try {
			continue
		}
		if prior.Runtime == nil || prior.Runtime.Name != request.Runtime.Name || prior.Runtime.Version != request.Runtime.Version || prior.Runtime.AdapterVersion != request.Runtime.AdapterVersion {
			return fmt.Errorf("runtime identity changed for this step visit; start a new execution")
		}
	}
	return nil
}
