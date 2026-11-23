package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"controls/adapters"
	"controls/execution"

	"github.com/stretchr/testify/require"
)

func TestPrivateProfilePreservesNativeDefaultsAndSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "agent")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "pi"), 0700))
	defaults := []byte(`{"defaultProvider":"custom","defaultModel":"native-model"}`)
	require.NoError(t, os.WriteFile(filepath.Join(source, "pi", "settings.json"), defaults, 0600))
	binary := filepath.Join(root, "pi")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho 1.0.3\n"), 0700))
	var profiles []string
	for range 2 {
		invocation := t.TempDir()
		prepared, err := (Adapter{}).Prepare(context.Background(), adapters.Request{Package: execution.PackageSnapshot{Source: source, Settings: json.RawMessage(`{"overrides":{}}`), Runtime: &execution.NativeRuntime{Name: "pi", Version: "=1.0.3", Executable: binary}}, Command: []string{"entrypoint", "--workspace", "selected"}, InvocationDir: invocation})
		require.NoError(t, err)
		require.Equal(t, []string{"entrypoint", "--workspace", "selected"}, prepared.Command)
		profile := prepared.Environment["PI_CODING_AGENT_DIR"]
		profiles = append(profiles, profile)
		content, err := os.ReadFile(filepath.Join(profile, "settings.json"))
		require.NoError(t, err)
		require.Equal(t, defaults, content)
		info, err := os.Stat(filepath.Join(profile, "settings.json"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		require.Equal(t, binary, prepared.Environment["AGENT_EXECUTABLE"])
		require.Equal(t, "1.0.3", prepared.Runtime.Version)
	}
	require.NotEqual(t, profiles[0], profiles[1])
	original, err := os.ReadFile(filepath.Join(source, "pi", "settings.json"))
	require.NoError(t, err)
	require.Equal(t, defaults, original)
}
func TestPiRejectsUnsupportedOverridesBeforeLaunch(t *testing.T) {
	err := (Adapter{}).Validate(execution.PackageSnapshot{Settings: json.RawMessage(`{"overrides":{"api_key":"fake-secret-value"}}`)})
	require.ErrorContains(t, err, "unsupported field")
	require.NotContains(t, err.Error(), "fake-secret-value")
}

func TestMalformedNativeProfileIsRejectedWithoutPrintingItsContents(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "pi"), 0700))
	binary := filepath.Join(root, "runner")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho 1.0.3\n"), 0700))
	for _, content := range []string{`null`, `["fake-secret-value"]`, `{"apiKey":"fake-secret-value"`} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "pi", "settings.json"), []byte(content), 0600))
		_, err := (Adapter{}).Prepare(context.Background(), adapters.Request{Package: execution.PackageSnapshot{Source: root, Runtime: &execution.NativeRuntime{Name: "pi", Executable: binary}, Settings: json.RawMessage(`{}`)}, InvocationDir: t.TempDir()})
		require.ErrorContains(t, err, "invalid pi profile settings.json")
		require.NotContains(t, err.Error(), "fake-secret-value")
	}
}

func TestPiMajorVersionCompatibility(t *testing.T) {
	cases := []struct {
		name, installed, required string
		accepted                  bool
	}{
		{"first stable", "1.0.0", "", true},
		{"previous pin", "1.0.3", "", true},
		{"patch upgrade", "1.0.4", "", true},
		{"minor upgrade", "1.1.0", ">=1.0.0 <2.0.0", true},
		{"later minor", "1.99.99", "", true},
		{"build metadata", "1.2.3+build.7", "", true},
		{"exact workflow pin", "1.0.3", "=1.0.3", true},
		{"workflow pin rejects upgrade", "1.0.4", "=1.0.3", false},
		{"narrow workflow range", "1.4.0", ">=1.2.0 <1.5.0", true},
		{"outside workflow range", "1.5.0", ">=1.2.0 <1.5.0", false},
		{"old major", "0.99.0", "", false},
		{"next major", "2.0.0", "", false},
		{"next major despite permissive workflow", "2.1.0", ">=1.0.0", false},
		{"prerelease", "1.2.0-rc.1", "", false},
		{"prerelease despite workflow opt in", "1.2.0-rc.1", ">=1.0.0-0 <2.0.0", false},
		{"next major prerelease", "2.0.0-rc.1", "", false},
		{"invalid probe", "not-a-version", "", false},
		{"invalid requirement", "1.2.0", "invalid", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "agent")
			require.NoError(t, os.MkdirAll(filepath.Join(source, "pi"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(source, "pi", "settings.json"), []byte(`{}`), 0600))
			binary := filepath.Join(root, "pi")
			require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho '"+tc.installed+"'\n"), 0700))
			invocation := filepath.Join(root, "invocation")
			prepared, err := (Adapter{}).Prepare(context.Background(), adapters.Request{
				Package: execution.PackageSnapshot{Source: source, Settings: json.RawMessage(`{}`), Runtime: &execution.NativeRuntime{Name: "pi", Version: tc.required, Executable: binary}},
				Command: []string{"launcher"}, InvocationDir: invocation,
			})
			if !tc.accepted {
				require.Error(t, err)
				_, statErr := os.Stat(filepath.Join(invocation, "profiles"))
				require.True(t, os.IsNotExist(statErr))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.installed, prepared.Runtime.Version)
			require.Equal(t, binary, prepared.Runtime.Executable)
			require.Equal(t, binary, prepared.Environment["AGENT_EXECUTABLE"])
		})
	}
}
