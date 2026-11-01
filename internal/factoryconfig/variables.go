package factoryconfig

import (
	"controls/execution"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Variables accepts only an object of strings; null is not an empty value.
type Variables map[string]string

func (v *Variables) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return fmt.Errorf("variables must be an object of strings")
	}
	values := make(Variables, len(fields))
	for name, raw := range fields {
		var text *string
		if err := json.Unmarshal(raw, &text); err != nil || text == nil {
			return fmt.Errorf("variable %q must be a string", name)
		}
		values[name] = *text
	}
	*v = values
	return nil
}

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// resolveVariables binds declared strings and command arguments before the
// configuration is persisted. It never evaluates a shell or reads environment.
func resolveVariables(definition Definition, project, executionID string) (Definition, error) {
	factoryDir, filesDir, err := executionDirectories(definition.FactoryDir, project, executionID)
	if err != nil {
		return definition, err
	}
	values := make(map[string]string, len(definition.Variables))
	active := make(map[string]bool)
	var resolveName func(string) (string, error)
	var expand func(string) (string, error)
	resolveName = func(name string) (string, error) {
		switch name {
		case "execution.project":
			return project, nil
		case "execution.factory_dir":
			return factoryDir, nil
		case "execution.files_dir":
			return filesDir, nil
		case "execution.id":
			if executionID == "" {
				return "${execution.id}", nil
			} // Load validates; LoadExecution binds.
			return executionID, nil
		}
		if !strings.HasPrefix(name, "variables.") {
			return "", fmt.Errorf("unknown reference %q", name)
		}
		key := strings.TrimPrefix(name, "variables.")
		value, exists := definition.Variables[key]
		if !exists {
			return "", fmt.Errorf("undefined variable %q", key)
		}
		if result, ok := values[key]; ok {
			return result, nil
		}
		if active[key] {
			return "", fmt.Errorf("variable cycle at %q", key)
		}
		active[key] = true
		result, err := expand(value)
		delete(active, key)
		if err != nil {
			return "", err
		}
		values[key] = result
		return result, nil
	}
	expand = func(text string) (string, error) {
		var result strings.Builder
		for len(text) > 0 {
			index := strings.Index(text, "$")
			if index < 0 {
				result.WriteString(text)
				break
			}
			result.WriteString(text[:index])
			text = text[index:]
			if strings.HasPrefix(text, "$${") {
				result.WriteString("${")
				text = text[3:]
				continue
			}
			if !strings.HasPrefix(text, "${") {
				result.WriteByte('$')
				text = text[1:]
				continue
			}
			end := strings.IndexByte(text, '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated variable reference")
			}
			value, err := resolveName(text[2:end])
			if err != nil {
				return "", err
			}
			result.WriteString(value)
			text = text[end+1:]
		}
		if strings.ContainsRune(result.String(), 0) {
			return "", fmt.Errorf("variable expansion contains a NUL byte")
		}
		return result.String(), nil
	}
	for name := range definition.Variables {
		if !variableName.MatchString(name) {
			return definition, fmt.Errorf("invalid variable name %q", name)
		}
		if _, err := resolveName("variables." + name); err != nil {
			return definition, err
		}
	}
	// Bind only explicit configuration fields, not arbitrary JSON package settings
	// or agent prompts. Bound values are inserted literally and never re-expanded.
	for _, packages := range [][]Package{definition.Packages, definition.Agents} {
		for i := range packages {
			for _, field := range []*string{&packages[i].Path, &packages[i].Entrypoint} {
				value, err := expand(*field)
				if err != nil {
					return definition, err
				}
				*field = value
			}
			for j := range packages[i].Args {
				value, err := expand(packages[i].Args[j])
				if err != nil {
					return definition, err
				}
				packages[i].Args[j] = value
			}
		}
	}
	allHooks := []*execution.Hooks{&definition.Hooks}
	for i := range definition.Workflow.Stages {
		stage := &definition.Workflow.Stages[i]
		allHooks = append(allHooks, &stage.Hooks)
		if stage.Loop != nil {
			for j := range stage.Loop.Steps {
				allHooks = append(allHooks, &stage.Loop.Steps[j].Hooks)
			}
		}
	}
	for i := range definition.Workflow.Steps {
		allHooks = append(allHooks, &definition.Workflow.Steps[i].Hooks)
	}
	for _, hooks := range allHooks {
		for _, list := range [][]execution.Hook{hooks.Before, hooks.After} {
			for i := range list {
				command, err := expand(list[i].Command)
				if err != nil {
					return definition, err
				}
				list[i].Command = command
				for j := range list[i].Arguments {
					value, err := expand(list[i].Arguments[j])
					if err != nil {
						return definition, err
					}
					list[i].Arguments[j] = value
				}
			}
		}
	}
	definition.Variables = values
	return definition, nil
}
