// Package pi owns Pi-specific profile translation and compatibility checks.
package pi

import (
	"context"
	"controls/adapters"
	"controls/execution"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Adapter struct{}

func (Adapter) Name() string { return "pi" }
func (Adapter) Capabilities() execution.RuntimeCapabilities {
	return execution.RuntimeCapabilities{OperatorInput: true, PeerMessages: true, IdleDelivery: true, Lifecycle: true, Usage: true, ToolEvents: true}
}

// Validate is side-effect free and runs before overrides are persisted.
func (Adapter) Validate(pkg execution.PackageSnapshot) error {
	_, active, err := decodeOverrides(pkg.Settings)
	if err != nil || !active {
		return err
	}
	var command struct {
		Args []string `json:"args"`
	}
	if json.Unmarshal(pkg.Settings, &command) != nil {
		return configurationError("args", "invalid_config", "invalid package arguments")
	}
	return validateArguments(command.Args)
}
func (Adapter) Prepare(ctx context.Context, request adapters.Request) (prepared adapters.Prepared, err error) {
	spec := request.Package.Runtime
	if spec == nil || spec.Name != "pi" {
		return prepared, configurationError("runtime", "invalid_config", "requires an explicit pi runtime")
	}
	overrides, active, err := decodeOverrides(request.Package.Settings)
	if err != nil {
		return prepared, err
	}
	if active && len(request.Command) == 0 {
		return prepared, configurationError("args", "invalid_config", "launch command is required")
	}
	if active {
		if err = validateArguments(request.Command[1:]); err != nil {
			return prepared, err
		}
	}
	executable := "pi"
	if spec.Executable != "" {
		executable = spec.Executable
	}
	executable, version, err := adapters.ExecutableVersion(ctx, executable, spec.Version, "=1.0.3")
	if err != nil {
		return prepared, err
	}
	profiles := filepath.Join(request.InvocationDir, "profiles")
	if err = os.MkdirAll(profiles, 0700); err != nil {
		return prepared, err
	}
	profile, err := os.MkdirTemp(profiles, "pi-")
	if err != nil {
		return prepared, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(profile)
		}
	}()
	files, err := filepath.Glob(filepath.Join(request.Package.Source, "pi", "*.json"))
	if err != nil {
		return prepared, err
	}
	if len(files) == 0 {
		return prepared, configurationError("profile", "invalid_config", "package requires a native pi profile")
	}
	contents := map[string][]byte{}
	for _, source := range files {
		data, err := os.ReadFile(source)
		if err != nil {
			return prepared, err
		}
		if _, err = decodeJSONObject(data, "invalid pi profile "+filepath.Base(source)); err != nil {
			return prepared, err
		}
		contents[filepath.Base(source)] = data
	}
	environment := map[string]string{"PI_CODING_AGENT_DIR": profile, "AGENT_EXECUTABLE": executable}
	provider, model := "", ""
	if active {
		provider, model, err = applyProfileOverrides(contents, overrides)
		if err != nil {
			return prepared, err
		}
		if overrides.APIKeyEnv != nil {
			value, exists := os.LookupEnv(*overrides.APIKeyEnv)
			if !exists || strings.TrimSpace(value) == "" {
				return prepared, configurationError("overrides.api_key_env", "missing_credentials", "environment variable "+*overrides.APIKeyEnv+" is unavailable to the execution service")
			}
			environment[*overrides.APIKeyEnv] = value
		}
	}
	for name, data := range contents {
		if err = os.WriteFile(filepath.Join(profile, name), data, 0600); err != nil {
			return prepared, err
		}
	}
	runtime := execution.RuntimeIdentity{Name: "pi", Version: version, AdapterVersion: "2", Executable: executable, Capabilities: Adapter{}.Capabilities()}
	if active {
		selected, metadata, err := checkNativeConfiguration(ctx, executable, profile, provider, model, overrides, credentialEnvironment(environment))
		if err != nil {
			return prepared, err
		}
		safe := map[string]any{"provider": selected.Provider, "model": selected.Model, "api": selected.API, "thinking": selected.Thinking, "reasoning": metadata.Reasoning, "context_window": metadata.ContextWindow, "max_tokens": metadata.MaxTokens}
		if endpoint, err := url.Parse(metadata.BaseURL); err == nil && endpoint.User == nil {
			endpoint.RawQuery = ""
			endpoint.Fragment = ""
			safe["base_url"] = endpoint.String()
		}
		if overrides.APIKeyEnv != nil {
			safe["api_key_env"] = *overrides.APIKeyEnv
		}
		runtime.Configuration = map[string]json.RawMessage{}
		for name, value := range safe {
			runtime.Configuration[name], _ = json.Marshal(value)
		}
		encoded, _ := json.Marshal(selected)
		environment["FACTORY_PI_EXPECTED_CONFIG"] = string(encoded)
		// Explicit native selection also wins over project-local default settings.
		request.Command = append(append([]string(nil), request.Command...), "--provider", provider, "--model", model, "--thinking", selected.Thinking)
	}
	complete = true
	return adapters.Prepared{Command: append([]string(nil), request.Command...), Environment: environment, InputMode: "pty", Runtime: runtime}, nil
}
func credentialEnvironment(environment map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range environment {
		if key != "PI_CODING_AGENT_DIR" && key != "AGENT_EXECUTABLE" {
			result[key] = value
		}
	}
	return result
}
func validateArguments(arguments []string) error {
	valueFlags := map[string]bool{"--workspace": true, "--skill": true, "--append-system-prompt": true, "--system-prompt": true, "-p": true, "--print": true, "--extension": true, "-e": true, "--tools": true, "-t": true, "--name": true, "-n": true, "--session-dir": true}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		name, _, _ := strings.Cut(argument, "=")
		switch name {
		case "--provider", "--model", "--models", "--thinking", "--api-key", "--session", "--session-id", "--resume", "--continue", "--fork", "-r", "-c":
			return configurationError("args", "config_conflict", "native selection, credentials and resume flags cannot be combined with overrides")
		}
		if valueFlags[name] && name == argument {
			index++
		}
	}
	return nil
}
