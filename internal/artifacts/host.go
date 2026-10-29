package artifacts

import (
	"context"

	"controls/execution"
)

// HostCollector applies local filesystem publication rules behind a runner port.
type HostCollector struct{ Root string }

func (HostCollector) Collect(_ context.Context, work execution.Work, extra []string) ([]execution.Artifact, error) {
	required := append([]string{}, work.Step.Outputs...)
	seen := map[string]bool{}
	for _, path := range required {
		seen[path] = true
	}
	for _, path := range extra {
		if !seen[path] {
			required = append(required, path)
			seen[path] = true
		}
	}
	return Collect(work.Project, required, nil)
}
