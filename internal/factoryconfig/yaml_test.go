package factoryconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestYAMLConfigurationPreservesContracts(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "agent"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "agent", "run.sh"), []byte("#!/bin/sh\n"), 0700))
	path := filepath.Join(root, "factory.yaml")
	text := `# Authored YAML, with arbitrary JSON-compatible package settings.
name: sample
variables:
  identifier: "001"
packages:
  - name: worker
    path: agent
    entrypoint: run.sh
    args: ["{prompt}"]
    runtime:
      name: pi
      version: "=1.0.3"
    overrides:
      model: example
      model_config:
        reasoning: true
        max_tokens: 8192
      labels: [on, yes, no]
workflow:
  start: build
  steps:
    - name: build
      type: run
      package: worker
      instructions: |-
        Implement the request.
        Preserve existing behavior.
      on:
        completed: $done
`
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	resolved, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "001", resolved.Configuration.Variables["identifier"])
	require.Equal(t, "Implement the request.\nPreserve existing behavior.", resolved.Configuration.Workflow.Steps[0].Instructions)
	require.Equal(t, "=1.0.3", resolved.Configuration.Packages[0].Runtime.Version)
	require.JSONEq(t, `["on","yes","no"]`, string(resolved.Definition.Packages[0].Overrides["labels"]))

	// Persisted execution JSON and its digest are independent of YAML formatting.
	canonical, err := json.Marshal(resolved.Definition)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, canonical, 0600))
	flow, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, resolved.Configuration, flow.Configuration)
	require.Equal(t, resolved.Digest, flow.Digest)
}

func TestYAMLRejectsAmbiguousOrUnsupportedDocuments(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"empty", "", "EOF"},
		{"null", "null", "expected a YAML mapping"},
		{"sequence", "- name: sample", "expected a YAML mapping"},
		{"multiple documents", "name: sample\n---\nname: other", "expected one YAML document"},
		{"duplicate key", "name: first\nname: second", "already defined"},
		{"nested duplicate", "packages:\n- overrides:\n    model: first\n    model: second", "already defined"},
		{"unknown root", "name: sample\nextends: other.yaml", `unknown field "extends"`},
		{"unknown nested", "name: sample\nworkflow:\n  unexpected: true", `unknown field "unexpected"`},
		{"typed key", "true: value", "mapping keys must be strings"},
		{"merge", "name: sample\nworkflow:\n  <<: {start: build}", "merge keys are not supported"},
		{"tag", "name: !custom sample", "unsupported YAML tag"},
		{"mapping tag", "!custom {name: sample}", "unsupported YAML tag"},
		{"sequence tag", "packages: !custom []", "unsupported YAML tag"},
		{"timestamp", "name: 2026-11-08", "unsupported YAML tag"},
		{"nonfinite", "packages:\n- overrides:\n    number: .inf", "unsupported value"},
		{"numeric variable", "variables:\n  identifier: 123", "must be a string"},
		{"cycle", "packages: &cycle [*cycle]", "anchor 'cycle' value contains itself"},
		{"syntax", "workflow: [", "yaml:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var definition Definition
			err := decodeYAML([]byte(tc.text), &definition)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestYAMLValueAliases(t *testing.T) {
	var definition Definition
	err := decodeYAML([]byte(`name: sample
packages:
  - name: author
    overrides: &settings
      model: example
  - name: reviewer
    overrides: *settings
`), &definition)
	require.NoError(t, err)
	require.Equal(t, definition.Packages[0].Overrides, definition.Packages[1].Overrides)
}

func TestDefaultConfigurationIsFactoryYAML(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	configuration := []byte(`name: default
workflow:
  start: approval
  steps:
    - name: approval
      type: decision
      actor: human
      on:
        approve: $done
`)
	// A legacy filename is not silently discovered.
	require.NoError(t, os.WriteFile("factory.json", configuration, 0600))
	_, err := Load("")
	require.ErrorContains(t, err, "factory.yaml")
	require.NoError(t, os.WriteFile("factory.yaml", configuration, 0600))
	resolved, err := Load("")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "factory.yaml"), resolved.Path)
	// Explicit paths are exact, not restricted to the default suffix.
	require.NoError(t, os.WriteFile("custom.yml", configuration, 0600))
	_, err = Load("custom.yml")
	require.NoError(t, err)
}
