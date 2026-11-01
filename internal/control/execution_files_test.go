package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"controls/execution"
	"controls/internal/packagehash"
	"controls/internal/testdb"
	"github.com/stretchr/testify/require"
)

type storageEngine struct {
	recordingEngine
	started []execution.StartRequest
}

func (e *storageEngine) Start(_ context.Context, request execution.StartRequest) (execution.Started, error) {
	e.started = append(e.started, request)
	_, err := os.Stat(request.Configuration.FilesDir)
	return execution.Started{ExecutionID: request.ExecutionID, RunID: "test"}, err
}

func TestExecutionFilesAreCreatedPinnedAndSurviveReload(t *testing.T) {
	db := testdb.Open(t)
	store, err := Open(db)
	require.NoError(t, err)
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "agent.sh"), []byte("#!/bin/sh\n"), 0700))
	config := `{"name":"storage","factory_dir":".factory","packages":[{"name":"worker","path":".","entrypoint":"agent.sh","args":["${execution.files_dir}"]}],"workflow":{"start":"run","steps":[{"name":"run","type":"run","package":"worker","on":{"done":"$done"}}]}}`
	path := filepath.Join(project, "factory.json")
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))
	engine := &storageEngine{}
	controller := Controller{Store: store, Engine: engine, Root: t.TempDir()}
	first, err := controller.Exec(context.Background(), ExecRequest{ConfigPath: path, Brief: "first", Actor: "test"})
	require.NoError(t, err)
	second, err := controller.Exec(context.Background(), ExecRequest{ConfigPath: path, Brief: "second", Actor: "test"})
	require.NoError(t, err)
	require.NotEqual(t, first.Configuration.FilesDir, second.Configuration.FilesDir)
	expected := filepath.Join(project, ".factory", "executions", first.ID, "files")
	require.Equal(t, expected, first.Configuration.FilesDir)
	info, err := os.Stat(filepath.Dir(expected))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	require.NoError(t, os.WriteFile(filepath.Join(expected, "spec.html"), []byte("draft"), 0600))
	reloaded, err := Open(db)
	require.NoError(t, err)
	saved, err := reloaded.Get(first.ID)
	require.NoError(t, err)
	require.Equal(t, expected, saved.Configuration.FilesDir)
	require.Equal(t, expected, engine.started[0].Configuration.FilesDir)
	require.NoError(t, packagehash.Verify(saved.Configuration.Packages[0]), "execution storage must not invalidate a package rooted at the project")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0600))
	saved, err = reloaded.Get(first.ID)
	require.NoError(t, err)
	require.Equal(t, expected, saved.Configuration.FilesDir, "authored config edits cannot relocate an existing execution")
}
