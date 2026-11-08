package factoryconfig

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadUsesProjectConfigDirectory(t *testing.T) {
	project := t.TempDir()
	packageRoot := filepath.Join(t.TempDir(), "agent")
	if err := os.Mkdir(packageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "start.sh"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := `{"name":"sample","agents":[{"name":"builder","path":` + strconv.Quote(packageRoot) + `,"entrypoint":"start.sh"}],"workflow":{"start":"build","steps":[{"name":"build","type":"agent","agent":"builder","on":{"completed":"$done"}}]}}`
	path := filepath.Join(project, "factory.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Project != project || resolved.Configuration.Packages[0].Source != packageRoot {
		t.Fatalf("unexpected project/package paths: %+v", resolved)
	}
}

func TestLoadRejectsExtendsAndUnsupportedSettings(t *testing.T) {
	for _, field := range []string{`"extends":"../base/factory.yaml",`, `"recipe":"README.md",`} {
		t.Run(field, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "factory.yaml")
			config := `{` + field + `"name":"sample"}`
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("expected unsupported-field error, got %v", err)
			}
		})
	}
}

func TestLoadStageLoopAndHumanDecision(t *testing.T) {
	project := t.TempDir()
	agent := filepath.Join(project, "agent")
	if err := os.Mkdir(agent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "run.sh"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := `{
		"name":"sample",
		"agents":[{"name":"worker","path":"agent","entrypoint":"run.sh"}],
		"workflow":{
			"start":"build",
			"stages":[{
				"name":"implementation",
				"loop":{"limit":2,"steps":[
					{"name":"build","type":"agent","agent":"worker","on":{"done":"review"}},
					{"name":"review","type":"agent","agent":"worker","on":{"completed":"approval","changes_requested":"build"}}
				]},
				"human":{"required":true,"step":"approval"}
			}],
			"steps":[{"name":"approval","type":"decision","actor":"human","on":{"approve":"$done","request_changes":"build"}}]
		}
	}`
	path := filepath.Join(project, "factory.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	stage := resolved.Configuration.Workflow.Stages[0]
	if stage.ID != "implementation" || stage.Loop.Limit != 2 || stage.Human.Step != "approval" || len(stage.Steps) != 2 {
		t.Fatalf("stage policy was not resolved: %+v", stage)
	}
	for _, step := range resolved.Configuration.Workflow.Steps {
		if step.Stage != "implementation" {
			t.Fatalf("step %q lost stage membership", step.ID)
		}
	}
}

func TestLoadRejectsStageStepsOutsideLoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "factory.yaml")
	config := `{"name":"sample","workflow":{"start":"build","stages":[{"name":"implementation","steps":["build"],"loop":{"limit":2}}],"steps":[]}}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), `unknown field "steps"`) {
		t.Fatalf("expected old stage shape to be rejected, got %v", err)
	}
}

func TestLoadRejectsIDInsteadOfName(t *testing.T) {
	for _, config := range []string{
		`{"name":"sample","workflow":{"start":"build","steps":[{"id":"build","type":"decision","actor":"human"}]}}`,
		`{"name":"sample","workflow":{"start":"build","stages":[{"id":"implementation","loop":{"limit":2,"steps":[{"name":"build","type":"decision","actor":"human"}]}}]}}`,
	} {
		t.Run(config, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "factory.yaml")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), `unknown field "id"`) {
				t.Fatalf("expected id field to be rejected, got %v", err)
			}
		})
	}
}

func TestExampleConfigLoadsWithAgentPackages(t *testing.T) {
	project := t.TempDir()
	for _, agent := range []string{"builder", "reviewer"} {
		path := filepath.Join(project, "agents", agent)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "start.sh"), []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "factory.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "factory.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	steps := resolved.Configuration.Workflow.Steps
	if len(steps) != 3 || steps[0].ID != "build" || steps[1].ID != "review" || steps[2].ID != "acceptance" {
		t.Fatalf("example step order was not preserved: %+v", steps)
	}
}

func TestConcurrentLoopConfiguration(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "agent")
	require.NoError(t, os.Mkdir(agent, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(agent, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	base := `{"name":"concurrent","agents":[{"name":"worker","path":"agent","entrypoint":"start.sh"}],"workflow":{"start":"a","stages":[{"name":"pair","loop":{"mode":"concurrent","limit":2,"steps":[{"name":"a","type":"agent","agent":"worker","on":{"ready":"$done","revise":"a"}},{"name":"b","type":"agent","agent":"worker","on":{"ready":"$done","revise":"a"}}]}}]}}`
	for _, tc := range []struct{ name, config, wantError string }{
		{"concurrent", base, ""},
		{"invalid mode", strings.Replace(base, `"mode":"concurrent"`, `"mode":"parallel"`, 1), "loop mode"},
		{"mixed repeat and exit", strings.Replace(base, `"ready":"$done"`, `"ready":"a"`, 1), ""},
		{"internal sequence forbidden", strings.Replace(base, `"ready":"$done"`, `"ready":"b"`, 1), "loop must be entered"},
		{"non-entry start", strings.Replace(base, `"start":"a"`, `"start":"b"`, 1), "first step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, "factory.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.config), 0600))
			resolved, err := Load(path)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.True(t, resolved.Configuration.Workflow.Stages[0].Concurrent())
		})
	}
}

func TestLoadPinsNestedStepBudgets(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	text := `{"name":"budget","agents":[{"name":"builder","path":".","entrypoint":"start.sh"}],"workflow":{"start":"build","stages":[{"name":"work","loop":{"limit":1,"steps":[{"name":"build","type":"agent","agent":"builder","budget":{"tokens":100,"warn_percent":80,"action":"pause"},"on":{"done":"$done"}}]}}],"steps":[]}}`
	path := filepath.Join(root, "factory.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	resolved, err := Load(path)
	require.NoError(t, err)
	require.EqualValues(t, 100, resolved.Configuration.Workflow.Steps[0].Budget.Tokens)
	require.Equal(t, "pause", resolved.Configuration.Workflow.Steps[0].Budget.Action)
	text = strings.Replace(text, `"warn_percent":80`, `"warn_percent":101`, 1)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	_, err = Load(path)
	require.ErrorContains(t, err, "warn_percent")
}

func TestLoadHooksAtExecutionStageAndStep(t *testing.T) {
	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, "start.sh"), []byte("#!/bin/sh\n"), 0700))
	hook := `{"name":"utility","command":"sh","arguments":["utility.sh","argument with spaces"]}`
	text := `{"name":"hooks","hooks":{"before":[` + hook + `]},"agents":[{"name":"builder","path":".","entrypoint":"start.sh"}],"workflow":{"start":"build","stages":[{"name":"work","hooks":{"after":[` + hook + `]},"loop":{"limit":1,"steps":[{"name":"build","type":"agent","agent":"builder","hooks":{"before":[` + hook + `]},"on":{"done":"$done"}}]}}],"steps":[]}}`
	path := filepath.Join(project, "factory.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	resolved, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "utility", resolved.Configuration.Hooks.Before[0].Name)
	require.Equal(t, "argument with spaces", resolved.Configuration.Workflow.Stages[0].Hooks.After[0].Arguments[1])
	require.Equal(t, "sh", resolved.Configuration.Workflow.Steps[0].Hooks.Before[0].Command)
}
