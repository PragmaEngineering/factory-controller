package control

import (
	"context"
	"controls/adapters"
	"controls/adapters/pi"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestAdapterValidationPrecedesExecutionPersistence(t *testing.T) {
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	file := filepath.Join(project, "factory.yaml")
	definition := `{"name":"fixture","factory_dir":".factory","packages":[{"name":"role","path":".","entrypoint":"start.sh","runtime":{"name":"pi","version":"=1.0.3"},"overrides":{"api_key":"private-test-value"}}],"workflow":{"start":"work","steps":[{"name":"work","type":"run","package":"role","on":{"completed":"$done"}}]}}`
	require.NoError(t, os.WriteFile(file, []byte(definition), 0600))
	controller := Controller{Engine: &recordingEngine{}, Adapters: adapters.Registry{"pi": pi.Adapter{}}}
	// A nil store is intentional: validation must not reach execution persistence.
	_, err := controller.Exec(context.Background(), ExecRequest{ConfigPath: file, Brief: "test"})
	require.ErrorContains(t, err, "invalid_config")
	require.NotContains(t, err.Error(), "private-test-value")
	require.NoDirExists(t, filepath.Join(project, ".factory"))
}
