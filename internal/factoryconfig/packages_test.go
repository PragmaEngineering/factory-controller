package factoryconfig

import (
	"controls/execution"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagesPinOverridesAndResolveBindings(t *testing.T) {
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	config := `{"name":"readiness","packages":[{"name":"classifier","path":".","entrypoint":"start.sh","overrides":{"model":"version-1","api_key_env":"MY_PROVIDER_KEY"}},{"name":"architect","path":".","entrypoint":"start.sh"}],"workflow":{"start":"evaluate","steps":[{"name":"evaluate","type":"run","package":"classifier","input_bindings":{"task":{"execution":"input"}},"on":{"completed":"plan"}},{"name":"plan","type":"run","package":"architect","input_bindings":{"response":{"step":"evaluate","output":"response"}},"on":{"completed":"$done"}}]}}`
	path := filepath.Join(project, "factory.yaml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))
	resolved, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, execution.RunStep, resolved.Configuration.Workflow.Steps[0].Kind)
	require.Equal(t, "classifier", resolved.Configuration.Workflow.Steps[0].Run.Package)
	require.Equal(t, "response", resolved.Configuration.Workflow.Steps[1].InputBindings["response"].Output)
	var settings map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(resolved.Configuration.Packages[0].Settings, &settings))
	require.JSONEq(t, `{"model":"version-1","api_key_env":"MY_PROVIDER_KEY"}`, string(settings["overrides"]))
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(config, "\"overrides\"", "\"with\"", 1)), 0600))
	_, err = Load(path)
	require.ErrorContains(t, err, "unknown field \"with\"")
	// Settings, as well as package bytes, participate in the pinned configuration digest.
	var definition map[string]any
	require.NoError(t, json.Unmarshal([]byte(config), &definition))
	definition["packages"].([]any)[0].(map[string]any)["overrides"].(map[string]any)["model"] = "version-2"
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	changed, err := Load(path)
	require.NoError(t, err)
	require.NotEqual(t, resolved.Digest, changed.Digest)
}

func TestRuntimeRequirementIsValidatedAndPinned(t *testing.T) {
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	config := `{"name":"native","packages":[{"name":"role","path":".","entrypoint":"start.sh","runtime":{"name":"pi","version":"=1.0.3","executable":"./tools/pi"}}],"workflow":{"start":"work","steps":[{"name":"work","type":"run","package":"role","on":{"done":"$done"}}]}}`
	file := filepath.Join(project, "factory.yaml")
	require.NoError(t, os.WriteFile(file, []byte(config), 0600))
	first, err := Load(file)
	require.NoError(t, err)
	require.Equal(t, "command", first.Configuration.Packages[0].Runner)
	require.Equal(t, "pi", first.Configuration.Packages[0].Runtime.Name)
	require.Equal(t, filepath.Join(project, "tools", "pi"), first.Configuration.Packages[0].Runtime.Executable)
	require.Equal(t, "=1.0.3", first.Configuration.Packages[0].Runtime.Version)
	changed := strings.Replace(config, "=1.0.3", ">=1.0.3 <2.0.0", 1)
	require.NoError(t, os.WriteFile(file, []byte(changed), 0600))
	second, err := Load(file)
	require.NoError(t, err)
	require.NotEqual(t, first.Digest, second.Digest)
	invalid := strings.Replace(config, "=1.0.3", "invalid", 1)
	require.NoError(t, os.WriteFile(file, []byte(invalid), 0600))
	_, err = Load(file)
	require.ErrorContains(t, err, "runtime.version")
}
