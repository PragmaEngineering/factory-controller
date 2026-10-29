package execution

import (
	"fmt"

	"github.com/google/uuid"
)

// StepVisitID identifies one visit to a step across the controller, runner,
// and event stream. Repeated visits have distinct IDs; retries retain one ID.
func StepVisitID(executionID string, step StepID, visit uint) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s/%s/%d", executionID, step, visit))).String()
}
