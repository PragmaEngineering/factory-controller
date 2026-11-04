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
		prepared, err := (Adapter{}).Prepare(context.Background(), adapters.Request{Package: execution.PackageSnapshot{Source: source, Settings: json.RawMessage(`{"with":{}}`), Runtime: &execution.NativeRuntime{Name: "pi", Version: "=1.0.3", Executable: binary}}, Command: []string{"entrypoint", "--workspace", "selected"}, InvocationDir: invocation})
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
	root := t.TempDir()
	binary := filepath.Join(root, "pi")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\necho 1.0.3\n"), 0700))
	_, err := (Adapter{}).Prepare(context.Background(), adapters.Request{Package: execution.PackageSnapshot{Runtime: &execution.NativeRuntime{Name: "pi", Executable: binary}, Settings: json.RawMessage(`{"with":{"model":"ignored-no-longer"}}`)}, InvocationDir: root})
	require.ErrorContains(t, err, "does not yet support with overrides")
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
