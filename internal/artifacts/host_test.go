package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"controls/execution"
	"github.com/stretchr/testify/require"
)

func TestCollectionAndPublicationUseExecutionFilesNotSource(t *testing.T) {
	project := t.TempDir()
	files, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(project, "spec.html"), []byte("wrong project copy"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(files, "spec.html"), []byte("execution draft"), 0600))
	work := execution.Work{ExecutionID: "one", Project: project, FilesDir: files, Step: execution.Step{Outputs: []string{"spec.html"}}}
	collector := HostCollector{Root: t.TempDir()}
	for _, attempt := range []uint{1, 2} {
		work.Try = attempt
		result, err := collector.Collect(context.Background(), work, nil)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(files, "spec.html"), result[0].URI)
	}
	published, err := collector.Publish(context.Background(), work, []string{"spec.html"}, "revision-one")
	require.NoError(t, err)
	require.Equal(t, []byte("execution draft"), published[0].Content)
	require.NoError(t, os.Remove(filepath.Join(files, "spec.html")))
	_, err = collector.Collect(context.Background(), work, nil)
	require.Error(t, err, "must not silently use the project copy")
}

func TestExecutionFilesCannotPublishOutsideTheirDirectory(t *testing.T) {
	files := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("private"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(files, "escape")))
	work := execution.Work{FilesDir: files, Step: execution.Step{Outputs: []string{"escape"}}}
	_, err := (HostCollector{}).Collect(context.Background(), work, nil)
	require.ErrorContains(t, err, "outside")
}
