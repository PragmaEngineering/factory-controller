package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type selection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	API      string `json:"api,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}
type nativeModelInfo struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	API           string `json:"api"`
	BaseURL       string `json:"baseUrl"`
	Reasoning     bool   `json:"reasoning"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
}

// preflight uses the exact executable and model/auth configuration, with resource
// discovery disabled. No prompt is sent, and Pi is offline throughout this probe.
func checkNativeConfiguration(ctx context.Context, executable, profile, provider, model string, overrides Overrides, environment map[string]string) (selection, nativeModelInfo, error) {
	directory, err := os.MkdirTemp(profile, "preflight-")
	if err != nil {
		return selection{}, nativeModelInfo{}, err
	}
	defer os.RemoveAll(directory)
	for _, name := range []string{"settings.json", "models.json", "auth.json"} {
		data, err := os.ReadFile(filepath.Join(profile, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return selection{}, nativeModelInfo{}, err
		}
		if name == "settings.json" {
			settings, err := decodeJSONObject(data, "settings.json")
			if err != nil {
				return selection{}, nativeModelInfo{}, err
			}
			for _, key := range []string{"packages", "extensions", "skills", "prompts", "themes"} {
				delete(settings, key)
			}
			data, _ = json.Marshal(settings)
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			return selection{}, nativeModelInfo{}, err
		}
	}
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(probe, executable, "--offline", "--mode", "rpc", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve")
	command.Dir = directory
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "FACTORY_") || strings.HasPrefix(name, "HATCHET_") || name == "DATABASE_URL" || name == "PI_CODING_AGENT_DIR" {
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "PI_CODING_AGENT_DIR="+directory, "PI_OFFLINE=1")
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Stdin = strings.NewReader("{\"id\":\"factory-preflight-state\",\"type\":\"get_state\"}\n{\"id\":\"factory-preflight-models\",\"type\":\"get_available_models\"}\n")
	command.Stderr = io.Discard
	stdout, err := command.StdoutPipe()
	if err != nil {
		return selection{}, nativeModelInfo{}, err
	}
	if command.Start() != nil {
		return selection{}, nativeModelInfo{}, configurationError("preflight", "runtime_preflight", "cannot start native configuration check")
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var selected nativeModelInfo
	var thinking string
	var available bool
	var gotState, gotModels bool
	for scanner.Scan() {
		var response struct {
			ID      string          `json:"id"`
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
		}
		if json.Unmarshal(scanner.Bytes(), &response) != nil {
			continue
		}
		switch response.ID {
		case "factory-preflight-state":
			if !response.Success {
				return selection{}, nativeModelInfo{}, configurationError("provider/model", "runtime_preflight", "native selection could not be resolved")
			}
			var state struct {
				Model    nativeModelInfo `json:"model"`
				Thinking string          `json:"thinkingLevel"`
			}
			if json.Unmarshal(response.Data, &state) != nil {
				return selection{}, nativeModelInfo{}, configurationError("preflight", "runtime_preflight", "invalid native state response")
			}
			selected, thinking, gotState = state.Model, state.Thinking, true
		case "factory-preflight-models":
			if !response.Success {
				return selection{}, nativeModelInfo{}, configurationError("credentials", "runtime_preflight", "native credential availability check failed")
			}
			var models struct {
				Models []nativeModelInfo `json:"models"`
			}
			if json.Unmarshal(response.Data, &models) != nil {
				return selection{}, nativeModelInfo{}, configurationError("preflight", "runtime_preflight", "invalid native model response")
			}
			for _, candidate := range models.Models {
				if candidate.Provider == provider && candidate.ID == model {
					available = true
				}
			}
			gotModels = true
		}
		if gotState && gotModels {
			break
		}
	}
	if !gotState || !gotModels {
		return selection{}, nativeModelInfo{}, configurationError("preflight", "runtime_preflight", "native configuration check failed or timed out")
	}
	if selected.Provider != provider || selected.ID != model {
		return selection{}, nativeModelInfo{}, configurationError("overrides.provider/model", "unknown_model", "selected model is unavailable; custom connections require model_config")
	}
	if !available {
		return selection{}, nativeModelInfo{}, configurationError("credentials", "missing_credentials", "selected provider has no usable native credentials")
	}
	if overrides.Thinking != nil && thinking != *overrides.Thinking {
		return selection{}, nativeModelInfo{}, configurationError("overrides.thinking", "unsupported_capability", "selected model does not support the requested thinking level")
	}
	if overrides.API != nil && selected.API != *overrides.API {
		return selection{}, nativeModelInfo{}, configurationError("overrides.api", "config_conflict", "native model resolved to a different API")
	}
	if overrides.BaseURL != nil && selected.BaseURL != *overrides.BaseURL {
		return selection{}, nativeModelInfo{}, configurationError("overrides.base_url", "config_conflict", "native model resolved to a different endpoint")
	}
	expected := selection{Provider: provider, Model: model, API: selected.API, Thinking: thinking}
	if overrides.BaseURL != nil {
		expected.BaseURL = *overrides.BaseURL
	}
	return expected, selected, nil
}
