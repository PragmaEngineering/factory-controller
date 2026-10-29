package hatchetengine

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Test binaries omit dependency versions from build metadata, which the
// embedded engine requires. A normal Go program retains that metadata.
func TestHumanDecisionReplaysIntoTheSameChildTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "./testdata/replay")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("durable replay regression failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "REPLAY_OK") {
		t.Fatalf("durable replay did not report success:\n%s", output)
	}
}
