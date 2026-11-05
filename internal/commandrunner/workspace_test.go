package commandrunner

import (
	"controls/execution"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAssignmentDoesNotPresentConfigurationDirectoryAsSourceWorkspace(t *testing.T) {
	work := execution.Work{Project: "/original/configuration", FilesDir: "/execution/documents", Step: execution.Step{ID: "build", Routes: map[execution.Outcome]execution.StepID{"completed": execution.Complete}}}
	prompt := buildPrompt(work, "thread")
	require.NotContains(t, prompt, work.Project)
	require.Contains(t, prompt, "working directory selected by your launcher")
	require.Contains(t, prompt, work.FilesDir)
	require.Contains(t, prompt, "Write workflow documents and evidence here, not in the project source")
	require.Contains(t, prompt, "source edits, checks")
}
