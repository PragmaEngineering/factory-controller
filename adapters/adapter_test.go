package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"controls/adapters"
	"controls/execution"

	"github.com/stretchr/testify/require"
)

func TestExplicitSelectionAndCapabilities(t *testing.T) {
	registry := adapters.Registry{"command": adapters.Command{}}
	selected, err := registry.Select(nil, false)
	require.NoError(t, err)
	require.Equal(t, "command", selected.Name())
	_, err = registry.Select(&execution.NativeRuntime{Name: "unknown"}, false)
	require.ErrorContains(t, err, "unsupported runtime")
	_, err = registry.Select(nil, true)
	require.ErrorContains(t, err, "concurrent peer delivery")
	_, err = registry.Select(&execution.NativeRuntime{Name: "command", Version: "not a version"}, false)
	require.ErrorContains(t, err, "runtime.version")
}
func TestExactExecutableVersionAndCompatibility(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "runner")
	for _, value := range []string{"1.0.3", "0.9.0", "2.0.0", "invalid"} {
		require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+value+"'\n"), 0700))
		path, version, err := adapters.ExecutableVersion(context.Background(), binary, ">=1.0.0 <1.1.0", ">=1.0.0 <2.0.0")
		if value == "1.0.3" {
			require.NoError(t, err)
			require.Equal(t, binary, path)
			require.Equal(t, value, version)
		} else {
			require.Error(t, err)
		}
	}
	_, _, err := adapters.ExecutableVersion(context.Background(), filepath.Join(root, "missing"), "", ">=1.0.0")
	require.ErrorContains(t, err, "unavailable")
}

func TestVersionProbeDoesNotInheritServiceCredentials(t *testing.T) {
	t.Setenv("HATCHET_CLIENT_TOKEN", "private-engine-credential")
	t.Setenv("PROVIDER_API_KEY", "private-model-credential")
	binary := filepath.Join(t.TempDir(), "runner")
	script := `#!/bin/sh
[ -z "${HATCHET_CLIENT_TOKEN:-}${PROVIDER_API_KEY:-}" ] || exit 3
echo 1.0.3
`
	require.NoError(t, os.WriteFile(binary, []byte(script), 0700))
	_, version, err := adapters.ExecutableVersion(context.Background(), binary, "=1.0.3", "=1.0.3")
	require.NoError(t, err)
	require.Equal(t, "1.0.3", version)
}
