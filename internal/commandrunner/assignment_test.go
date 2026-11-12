package commandrunner

import (
	"context"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/factoryconfig"
	"controls/internal/sessions"
	"controls/internal/testdb"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadinessUsesAssignmentMCPWithoutInvocationFiles(t *testing.T) {
	registry, err := filepath.Abs("../../../registry")
	require.NoError(t, err)
	response, err := os.ReadFile(filepath.Join(registry, "tests/fixtures/readiness-response.json"))
	if os.IsNotExist(err) {
		t.Skip("sibling Registry checkout required")
	}
	require.NoError(t, err)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-provider-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	defer provider.Close()
	t.Setenv("READINESS_TEST_KEY", "fixture-provider-key")
	root := t.TempDir()
	db := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	communication, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	stop, err := communication.Listen(ctx)
	require.NoError(t, err)
	defer stop()
	manager, err := sessions.Open(filepath.Join(root, "sessions"), "", db)
	require.NoError(t, err)
	defer manager.Shutdown(context.Background())
	definition := map[string]any{"name": "readiness-mcp", "packages": []any{map[string]any{"name": "readiness", "path": filepath.Join(registry, "classifiers/task-readiness"), "entrypoint": "start.sh", "protocol": "mcp", "overrides": map[string]any{"endpoint": provider.URL, "api_key_env": "READINESS_TEST_KEY"}}}, "workflow": map[string]any{"start": "assess", "steps": []any{map[string]any{"name": "assess", "type": "run", "package": "readiness", "input_bindings": map[string]any{"task": map[string]any{"execution": "input"}}, "on": map[string]any{"completed": "$done"}}}}}
	raw, _ := json.Marshal(definition)
	path := filepath.Join(root, "factory.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	resolved, err := factoryconfig.Load(path)
	require.NoError(t, err)
	work := execution.Work{ExecutionID: uuid.NewString(), Project: root, Brief: "Investigate gateway latency", Step: resolved.Configuration.Workflow.Steps[0], Package: resolved.Configuration.Packages[0], Visit: 1, Try: 1}
	runner := Runner{Comms: communication, Sessions: manager, Recorder: testRecorder{}, Root: root}
	result, err := runner.Run(ctx, work)
	require.NoError(t, err)
	require.Equal(t, execution.Outcome("completed"), result.Outcome)
	require.Contains(t, string(result.Data["response"]), "clarify")
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		for _, name := range []string{"inputs.json", "package-config.json", "prompt.md"} {
			require.NotEqual(t, name, info.Name())
		}
		return nil
	}))
}
