package factoryconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func variableFixture(t *testing.T, variables map[string]string, arguments []string, factoryDir string) string {
	t.Helper()
	if variables == nil {
		variables = map[string]string{}
	}
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "agent.sh"), []byte("#!/bin/sh\n"), 0700))
	definition := map[string]any{"name": "variables", "factory_dir": factoryDir, "variables": variables,
		"packages": []any{map[string]any{"name": "worker", "path": ".", "entrypoint": "agent.sh", "args": arguments}},
		"hooks":    map[string]any{"before": []any{map[string]any{"name": "prepare", "command": "python3", "arguments": arguments}}},
		"workflow": map[string]any{"start": "run", "steps": []any{map[string]any{"name": "run", "type": "run", "package": "worker", "on": map[string]string{"done": "$done"}}}}}
	raw, err := json.Marshal(definition)
	require.NoError(t, err)
	path := filepath.Join(project, "factory.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	return path
}

func TestVariablesAndExecutionStorageResolveOnce(t *testing.T) {
	path := variableFixture(t, map[string]string{"worktree": "${execution.factory_dir}/worktrees/${execution.id}", "literal": "$${execution.id}"}, []string{"${variables.worktree}", "${execution.files_dir}", "${variables.literal}", "{prompt}"}, ".factory")
	one, err := LoadExecution(path, "run-one")
	require.NoError(t, err)
	two, err := LoadExecution(path, "run-two")
	require.NoError(t, err)
	root := filepath.Dir(path)
	require.Equal(t, filepath.Join(root, ".factory", "executions", "run-one", "files"), one.Configuration.FilesDir)
	require.Equal(t, filepath.Join(root, ".factory", "worktrees", "run-one"), one.Configuration.Variables["worktree"])
	require.Equal(t, "${execution.id}", one.Configuration.Variables["literal"])
	require.NotEqual(t, one.Configuration.FilesDir, two.Configuration.FilesDir)
	require.NotEqual(t, one.Digest, two.Digest)
	var settings struct {
		Args []string `json:"args"`
	}
	require.NoError(t, json.Unmarshal(one.Configuration.Packages[0].Settings, &settings))
	require.Equal(t, []string{one.Configuration.Variables["worktree"], one.Configuration.FilesDir, "${execution.id}", "{prompt}"}, settings.Args)
	require.Equal(t, settings.Args, one.Configuration.Hooks.Before[0].Arguments)
	// Resolution does not rewrite the authored configuration.
	template, err := Load(path)
	require.NoError(t, err)
	require.Contains(t, template.Configuration.FilesDir, "${execution.id}")
}

func TestVariableValidationRejectsErrorsBeforeExecution(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"missing": {"a": "${variables.absent}"}, "cycle": {"a": "${variables.b}", "b": "${variables.a}"},
		"namespace": {"a": "${env.HOME}"}, "unterminated": {"a": "${execution.id"}, "invalid name": {"bad-name": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			path := variableFixture(t, vars, nil, "")
			_, err := LoadExecution(path, "run")
			require.Error(t, err)
		})
	}
	path := variableFixture(t, nil, []string{"${variables.absent}"}, "")
	_, err := LoadExecution(path, "run")
	require.ErrorContains(t, err, "undefined variable")
}

func TestFactoryDirectoryDefaultsAndExplicitLocations(t *testing.T) {
	for _, directory := range []string{"", "local-state", t.TempDir()} {
		path := variableFixture(t, nil, nil, directory)
		config, err := LoadExecution(path, "run")
		require.NoError(t, err)
		expected := directory
		if expected == "" {
			expected = ".factory"
		}
		if !filepath.IsAbs(expected) {
			expected = filepath.Join(filepath.Dir(path), expected)
		}
		require.Equal(t, expected, config.Configuration.FactoryDir)
		require.Equal(t, filepath.Join(expected, "executions", "run", "files"), config.Configuration.FilesDir)
	}
}

func TestVariablesRejectNonStringValues(t *testing.T) {
	for _, raw := range []string{`null`, `{"value":null}`, `{"value":123}`, `{"value":true}`, `[]`} {
		var variables Variables
		require.Error(t, json.Unmarshal([]byte(raw), &variables), raw)
	}
}
