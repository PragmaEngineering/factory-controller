package factoryconfig

import (
	"controls/execution"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestPackagesPinOverridesAndResolveBindings(t *testing.T) {
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	config := `{"name":"readiness","packages":[{"name":"classifier","path":".","entrypoint":"start.sh","with":{"model":"version-1","api_key_env":"MY_PROVIDER_KEY"}},{"name":"architect","path":".","entrypoint":"start.sh"}],"workflow":{"start":"evaluate","steps":[{"name":"evaluate","type":"run","package":"classifier","input_bindings":{"task":{"execution":"input"}},"on":{"completed":"plan"}},{"name":"plan","type":"run","package":"architect","input_bindings":{"response":{"step":"evaluate","output":"response"}},"on":{"completed":"$done"}}]}}`
	path := filepath.Join(project, "factory.json")
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))
	resolved, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, execution.RunStep, resolved.Configuration.Workflow.Steps[0].Kind)
	require.Equal(t, "classifier", resolved.Configuration.Workflow.Steps[0].Run.Package)
	require.Equal(t, "response", resolved.Configuration.Workflow.Steps[1].InputBindings["response"].Output)
	var settings map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(resolved.Configuration.Packages[0].Settings, &settings))
	require.JSONEq(t, `{"model":"version-1","api_key_env":"MY_PROVIDER_KEY"}`, string(settings["with"]))
	// Settings, as well as package bytes, participate in the pinned configuration digest.
	var definition map[string]any
	require.NoError(t, json.Unmarshal([]byte(config), &definition))
	definition["packages"].([]any)[0].(map[string]any)["with"].(map[string]any)["model"] = "version-2"
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	changed, err := Load(path)
	require.NoError(t, err)
	require.NotEqual(t, resolved.Digest, changed.Digest)
}
