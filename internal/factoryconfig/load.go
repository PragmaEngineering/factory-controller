package factoryconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"controls/execution"
	"controls/internal/packagehash"
)

// Definition is the project-facing factory.yaml format. Paths in it are
// resolved relative to the file, never relative to the CLI process.
type Definition struct {
	FactoryDir string          `json:"factory_dir,omitempty"`
	Variables  Variables       `json:"variables,omitempty"`
	Hooks      execution.Hooks `json:"hooks,omitempty"`
	Name       string          `json:"name"`
	Agents     []Package       `json:"agents,omitempty"`
	Packages   []Package       `json:"packages,omitempty"`
	Workflow   struct {
		Start  string  `json:"start"`
		Stages []Stage `json:"stages,omitempty"`
		Steps  []Step  `json:"steps"`
	} `json:"workflow"`
}

type Stage struct {
	Hooks execution.Hooks `json:"hooks,omitempty"`
	Name  string          `json:"name"`
	Loop  *Loop           `json:"loop"`
	Human *Human          `json:"human,omitempty"`
}

type Loop struct {
	Mode  string `json:"mode,omitempty"`
	Limit uint   `json:"limit"`
	Steps []Step `json:"steps"`
}

type Human struct {
	Required bool   `json:"required"`
	Step     string `json:"step,omitempty"`
}

// Package selects an independently runnable bundle. Overrides is interpreted by its selected runtime or bundle.
type Package struct {
	Runtime    *execution.NativeRuntime   `json:"runtime,omitempty"`
	Overrides  map[string]json.RawMessage `json:"overrides,omitempty"`
	Name       string                     `json:"name"`
	Path       string                     `json:"path"`
	Entrypoint string                     `json:"entrypoint"`
	Args       []string                   `json:"args,omitempty"`
}

type Step struct {
	Package       string                            `json:"package,omitempty"`
	InputBindings map[string]execution.InputBinding `json:"input_bindings,omitempty"`
	Hooks         execution.Hooks                   `json:"hooks,omitempty"`
	Budget        *execution.Budget                 `json:"budget,omitempty"`
	Name          string                            `json:"name"`
	Type          string                            `json:"type"`
	Agent         string                            `json:"agent,omitempty"`
	Title         string                            `json:"title,omitempty"`
	Instructions  string                            `json:"instructions,omitempty"`
	Inputs        []string                          `json:"inputs,omitempty"`
	Outputs       []string                          `json:"outputs,omitempty"`
	OutputsOn     []execution.Outcome               `json:"outputs_on,omitempty"`
	Artifacts     []string                          `json:"artifacts,omitempty"`
	Actor         string                            `json:"actor,omitempty"`
	On            map[string]string                 `json:"on"`
}

type Resolved struct {
	Path          string
	Project       string
	Definition    Definition
	Configuration execution.Configuration
	Digest        string
}

func Load(path string) (Resolved, error) { return load(path, "") }

// LoadExecution resolves execution references once before persistence.
func LoadExecution(path, executionID string) (Resolved, error) {
	if executionID == "" {
		return Resolved{}, fmt.Errorf("execution ID is required")
	}
	return load(path, executionID)
}

func load(path, executionID string) (Resolved, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return Resolved{}, err
		}
		path = filepath.Join(path, "factory.yaml")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Resolved{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Resolved{}, fmt.Errorf("read factory config %s: %w", abs, err)
	}
	var definition Definition
	if err := decodeYAML(data, &definition); err != nil {
		return Resolved{}, fmt.Errorf("parse factory config %s: %w", abs, err)
	}
	if definition.Name == "" {
		return Resolved{}, fmt.Errorf("%s: name is required", abs)
	}
	definition, err = resolveVariables(definition, filepath.Dir(abs), executionID)
	if err != nil {
		return Resolved{}, fmt.Errorf("%s: %w", abs, err)
	}
	factoryDir, filesDir, err := executionDirectories(definition.FactoryDir, filepath.Dir(abs), executionID)
	if err != nil {
		return Resolved{}, err
	}
	config := execution.Configuration{FactoryDir: factoryDir, FilesDir: filesDir, Variables: definition.Variables, Hooks: definition.Hooks, Workflow: execution.Workflow{
		Name:  definition.Name,
		Start: execution.StepID(definition.Workflow.Start),
	}}
	stageByStep := make(map[string]string)
	topLevelSteps := make(map[string]Step, len(definition.Workflow.Steps))
	for _, step := range definition.Workflow.Steps {
		if _, exists := topLevelSteps[step.Name]; exists {
			return Resolved{}, fmt.Errorf("%s: duplicate step %q", abs, step.Name)
		}
		topLevelSteps[step.Name] = step
	}
	steps := make([]Step, 0, len(definition.Workflow.Steps))
	usedTopLevel := make(map[string]bool)
	for _, stage := range definition.Workflow.Stages {
		if stage.Loop == nil {
			return Resolved{}, fmt.Errorf("%s: stage %q requires a loop containing its steps", abs, stage.Name)
		}
		resolved := execution.Stage{Hooks: stage.Hooks, ID: stage.Name, Loop: &execution.LoopPolicy{Limit: stage.Loop.Limit, Mode: stage.Loop.Mode}}
		for _, step := range stage.Loop.Steps {
			resolved.Steps = append(resolved.Steps, execution.StepID(step.Name))
			stageByStep[step.Name] = stage.Name
			steps = append(steps, step)
		}
		if stage.Human != nil {
			resolved.Human = &execution.HumanPolicy{Required: stage.Human.Required, Step: execution.StepID(stage.Human.Step)}
			if stage.Human.Step != "" {
				stageByStep[stage.Human.Step] = stage.Name
				if decision, exists := topLevelSteps[stage.Human.Step]; exists {
					steps = append(steps, decision)
					usedTopLevel[stage.Human.Step] = true
				}
			}
		}
		config.Workflow.Stages = append(config.Workflow.Stages, resolved)
	}
	if start, exists := topLevelSteps[definition.Workflow.Start]; exists && !usedTopLevel[start.Name] {
		steps = append([]Step{start}, steps...)
		usedTopLevel[start.Name] = true
	}
	for _, step := range definition.Workflow.Steps {
		if !usedTopLevel[step.Name] {
			steps = append(steps, step)
		}
	}
	packages := append(append([]Package{}, definition.Packages...), definition.Agents...)
	registered := make(map[string]Package, len(packages))
	for _, pkg := range packages {
		if pkg.Name == "" || pkg.Path == "" || pkg.Entrypoint == "" {
			return Resolved{}, fmt.Errorf("%s: each package needs name, path, and entrypoint", abs)
		}
		if _, exists := registered[pkg.Name]; exists {
			return Resolved{}, fmt.Errorf("%s: duplicate package %q", abs, pkg.Name)
		}
		packagePath := pkg.Path
		if !filepath.IsAbs(packagePath) {
			packagePath = filepath.Join(filepath.Dir(abs), packagePath)
		}
		root, err := filepath.Abs(packagePath)
		if err != nil {
			return Resolved{}, err
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return Resolved{}, fmt.Errorf("%s: package %q directory %s is unavailable", abs, pkg.Name, root)
		}
		entrypoint := filepath.Join(root, pkg.Entrypoint)
		if info, err := os.Stat(entrypoint); err != nil || info.IsDir() {
			return Resolved{}, fmt.Errorf("%s: package %q entrypoint %s is unavailable", abs, pkg.Name, entrypoint)
		}
		if pkg.Runtime != nil && pkg.Runtime.Executable != "" {
			runtime := *pkg.Runtime
			if !filepath.IsAbs(runtime.Executable) && strings.ContainsAny(runtime.Executable, `/\`) {
				runtime.Executable = filepath.Join(filepath.Dir(abs), runtime.Executable)
			}
			pkg.Runtime = &runtime
		}
		pkg.Path = root
		registered[pkg.Name] = pkg
		settings, _ := json.Marshal(struct {
			Overrides  map[string]json.RawMessage `json:"overrides,omitempty"`
			Entrypoint string                     `json:"entrypoint"`
			Args       []string                   `json:"args,omitempty"`
		}{pkg.Overrides, pkg.Entrypoint, pkg.Args})
		var excluded []string
		if relative, err := filepath.Rel(root, factoryDir); err == nil && filepath.IsLocal(relative) {
			if relative == "." {
				return Resolved{}, fmt.Errorf("package %q source cannot equal factory_dir", pkg.Name)
			}
			excluded = []string{factoryDir}
		}
		digest, err := packagehash.Directory(root, excluded...)
		if err != nil {
			return Resolved{}, fmt.Errorf("hash package %q: %w", pkg.Name, err)
		}
		config.Packages = append(config.Packages, execution.PackageSnapshot{
			ExcludedPaths: excluded, Name: pkg.Name, Source: root, Digest: digest, Runner: "command", Runtime: pkg.Runtime, Settings: settings,
		})
	}
	for _, step := range steps {
		title := step.Title
		if title == "" {
			title = step.Name
		}
		resolved := execution.Step{Hooks: step.Hooks, Budget: step.Budget, ID: execution.StepID(step.Name), Stage: stageByStep[step.Name], Title: title, Instructions: step.Instructions,
			InputBindings: step.InputBindings, Inputs: step.Inputs, Outputs: step.Outputs, OutputsOn: step.OutputsOn, Routes: make(map[execution.Outcome]execution.StepID, len(step.On))}
		for outcome, target := range step.On {
			switch target {
			case "$done":
				target = string(execution.Complete)
			case "$blocked":
				target = string(execution.Fail)
			}
			resolved.Routes[execution.Outcome(outcome)] = execution.StepID(target)
		}
		switch step.Type {
		case "agent", "run":
			selected := step.Package
			if step.Type == "agent" {
				selected = step.Agent
			}
			if selected == "" || (step.Type == "run" && step.Agent != "") || (step.Type == "agent" && step.Package != "") {
				return Resolved{}, fmt.Errorf("%s: step %q must select its package with %s", abs, step.Name, map[string]string{"run": "package", "agent": "agent"}[step.Type])
			}
			if _, exists := registered[selected]; !exists {
				return Resolved{}, fmt.Errorf("%s: step %q references unknown package %q", abs, step.Name, selected)
			}
			resolved.Kind = execution.RunStep
			resolved.Run = &execution.RunSpec{Package: selected}
		case "decision":
			if step.Agent != "" || step.Package != "" {
				return Resolved{}, fmt.Errorf("%s: decision step %q cannot reference a runnable package", abs, step.Name)
			}
			resolved.Kind = execution.DecisionStep
			options := make([]execution.Outcome, 0, len(step.On))
			for outcome := range step.On {
				options = append(options, execution.Outcome(outcome))
			}
			sort.Slice(options, func(i, j int) bool { return options[i] < options[j] })
			prompt := title
			if len(step.Artifacts) > 0 {
				prompt += ": review " + strings.Join(step.Artifacts, ", ")
			}
			resolved.Decision = &execution.DecisionSpec{Authority: step.Actor, Prompt: prompt, Options: options, Artifacts: step.Artifacts}
		default:
			return Resolved{}, fmt.Errorf("%s: step %q has unsupported type %q", abs, step.Name, step.Type)
		}
		config.Workflow.Steps = append(config.Workflow.Steps, resolved)
	}
	if err := execution.Validate(config); err != nil {
		return Resolved{}, fmt.Errorf("%s: %w", abs, err)
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return Resolved{}, err
	}
	digest := sha256.Sum256(canonical)
	return Resolved{Path: abs, Project: filepath.Dir(abs), Definition: definition,
		Configuration: config, Digest: hex.EncodeToString(digest[:])}, nil
}
