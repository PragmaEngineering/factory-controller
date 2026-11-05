package pi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ConfigError classifies preparation failures without echoing native output or credentials.
type ConfigError struct{ Field, Code, Message string }

func (e *ConfigError) Error() string {
	return fmt.Sprintf("pi %s: %s (%s)", e.Field, e.Message, e.Code)
}
func configurationError(field, code, message string) error {
	return &ConfigError{Field: field, Code: code, Message: message}
}

// Overrides are deliberately finite. They are not a native JSON patch language.
type Overrides struct {
	Provider    *string      `json:"provider,omitempty"`
	Model       *string      `json:"model,omitempty"`
	BaseURL     *string      `json:"base_url,omitempty"`
	API         *string      `json:"api,omitempty"`
	APIKeyEnv   *string      `json:"api_key_env,omitempty"`
	Thinking    *string      `json:"thinking,omitempty"`
	ModelConfig *ModelConfig `json:"model_config,omitempty"`
}
type ModelConfig struct {
	Name          string     `json:"name,omitempty"`
	Reasoning     *bool      `json:"reasoning"`
	ContextWindow int        `json:"context_window"`
	MaxTokens     int        `json:"max_tokens"`
	Input         []string   `json:"input,omitempty"`
	Cost          *ModelCost `json:"cost,omitempty"`
}
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func decodeOverrides(settings json.RawMessage) (Overrides, bool, error) {
	var envelope struct {
		Overrides json.RawMessage `json:"overrides"`
		Legacy    json.RawMessage `json:"with"`
	}
	if json.Unmarshal(settings, &envelope) != nil {
		return Overrides{}, false, configurationError("overrides", "invalid_config", "invalid package settings")
	}
	if len(envelope.Legacy) > 0 {
		return Overrides{}, false, configurationError("overrides", "invalid_config", "unsupported package field with; use overrides")
	}
	if len(envelope.Overrides) == 0 {
		return Overrides{}, false, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope.Overrides, &fields) != nil || fields == nil {
		return Overrides{}, false, configurationError("overrides", "invalid_config", "expected an object")
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Overrides{}, false, configurationError("overrides", "invalid_config", "null values are not supported")
		}
	}
	var overrides Overrides
	decoder := json.NewDecoder(bytes.NewReader(envelope.Overrides))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&overrides) != nil {
		return Overrides{}, false, configurationError("overrides", "invalid_config", "unsupported field or invalid value type")
	}
	for name, value := range map[string]*string{"provider": overrides.Provider, "model": overrides.Model, "base_url": overrides.BaseURL, "api": overrides.API, "api_key_env": overrides.APIKeyEnv, "thinking": overrides.Thinking} {
		if value != nil && (strings.TrimSpace(*value) == "" || strings.ContainsAny(*value, "\x00\r\n")) {
			return Overrides{}, false, configurationError("overrides."+name, "invalid_config", "requires a nonempty single-line value")
		}
	}
	if overrides.APIKeyEnv != nil && !envName.MatchString(*overrides.APIKeyEnv) {
		return Overrides{}, false, configurationError("overrides.api_key_env", "invalid_config", "expected an environment-variable name")
	}
	if overrides.APIKeyEnv != nil {
		name := *overrides.APIKeyEnv
		if strings.HasPrefix(name, "FACTORY_") || strings.HasPrefix(name, "HATCHET_") || strings.HasPrefix(name, "PI_") || containsString([]string{"DATABASE_URL", "AGENT_EXECUTABLE", "NODE_OPTIONS", "PATH", "HOME"}, name) {
			return Overrides{}, false, configurationError("overrides.api_key_env", "invalid_config", "reserved environment-variable name")
		}
	}
	if overrides.BaseURL != nil {
		endpoint, err := url.Parse(*overrides.BaseURL)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return Overrides{}, false, configurationError("overrides.base_url", "invalid_config", "expected an HTTP(S) endpoint without credentials, query or fragment")
		}
	}
	if overrides.API != nil && !containsString([]string{"openai-completions", "openai-responses", "anthropic-messages", "google-generative-ai"}, *overrides.API) {
		return Overrides{}, false, configurationError("overrides.api", "unsupported_protocol", "unsupported API protocol")
	}
	if overrides.Thinking != nil && !containsString([]string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}, *overrides.Thinking) {
		return Overrides{}, false, configurationError("overrides.thinking", "invalid_config", "unsupported thinking level")
	}
	if model := overrides.ModelConfig; model != nil {
		if model.Reasoning == nil || model.ContextWindow <= 0 || model.MaxTokens <= 0 || model.MaxTokens > model.ContextWindow {
			return Overrides{}, false, configurationError("overrides.model_config", "invalid_config", "requires reasoning, positive context_window and max_tokens not exceeding context_window")
		}
		for _, input := range model.Input {
			if input != "text" && input != "image" {
				return Overrides{}, false, configurationError("overrides.model_config.input", "invalid_config", "unsupported input modality")
			}
		}
		if cost := model.Cost; cost != nil && (cost.Input < 0 || cost.Output < 0 || cost.CacheRead < 0 || cost.CacheWrite < 0) {
			return Overrides{}, false, configurationError("overrides.model_config.cost", "invalid_config", "prices cannot be negative")
		}
	}
	return overrides, len(fields) > 0, nil
}
func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

type nativeObject map[string]json.RawMessage

func decodeJSONObject(raw []byte, field string) (nativeObject, error) {
	var result nativeObject
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return nil, configurationError(field, "invalid_config", "expected a JSON object")
	}
	return result, nil
}
func setNativeField(target nativeObject, key string, value any) { target[key], _ = json.Marshal(value) }
func nativeString(target nativeObject, key string) string {
	var value string
	_ = json.Unmarshal(target[key], &value)
	return value
}

// applyProfileOverrides edits only the selected connection/model; unrelated native profile fields survive.
func applyProfileOverrides(profile map[string][]byte, overrides Overrides) (string, string, error) {
	settings, err := decodeJSONObject(profile["settings.json"], "settings.json")
	if err != nil {
		return "", "", err
	}
	oldProvider := nativeString(settings, "defaultProvider")
	provider, model := oldProvider, nativeString(settings, "defaultModel")
	if overrides.Provider != nil {
		provider = *overrides.Provider
	}
	if provider != oldProvider && overrides.Model == nil {
		return "", "", configurationError("overrides.model", "invalid_config", "a provider change requires an explicit model")
	}
	if overrides.Model != nil {
		model = *overrides.Model
	}
	if provider == "" || model == "" {
		return "", "", configurationError("provider/model", "invalid_config", "native defaults or overrides must select a provider and model")
	}
	setNativeField(settings, "defaultProvider", provider)
	setNativeField(settings, "defaultModel", model)
	if overrides.Thinking != nil {
		setNativeField(settings, "defaultThinkingLevel", *overrides.Thinking)
	}
	profile["settings.json"], _ = json.MarshalIndent(settings, "", "  ")
	models := nativeObject{}
	if raw, ok := profile["models.json"]; ok {
		models, err = decodeJSONObject(raw, "models.json")
		if err != nil {
			return "", "", err
		}
	}
	providers := nativeObject{}
	if raw, ok := models["providers"]; ok {
		providers, err = decodeJSONObject(raw, "models.json.providers")
		if err != nil {
			return "", "", err
		}
	}
	connection := nativeObject{}
	if raw, ok := providers[provider]; ok {
		connection, err = decodeJSONObject(raw, "models.json.providers")
		if err != nil {
			return "", "", err
		}
	}
	custom := overrides.BaseURL != nil || overrides.API != nil || overrides.ModelConfig != nil
	if custom && len(connection) == 0 && (overrides.BaseURL == nil || overrides.API == nil || overrides.APIKeyEnv == nil || overrides.ModelConfig == nil) {
		return "", "", configurationError("overrides", "invalid_config", "a new custom connection requires base_url, api, api_key_env and model_config")
	}
	if overrides.BaseURL != nil {
		setNativeField(connection, "baseUrl", *overrides.BaseURL)
	}
	if overrides.API != nil {
		setNativeField(connection, "api", *overrides.API)
	}
	if overrides.APIKeyEnv != nil {
		setNativeField(connection, "apiKey", "$"+*overrides.APIKeyEnv)
		setNativeField(connection, "authHeader", true)
		// Native stored authentication takes precedence over models.json.
		if raw, ok := profile["auth.json"]; ok {
			auth, err := decodeJSONObject(raw, "auth.json")
			if err != nil {
				return "", "", err
			}
			delete(auth, provider)
			profile["auth.json"], _ = json.MarshalIndent(auth, "", "  ")
		}
	}
	if overrides.APIKeyEnv != nil || overrides.BaseURL != nil {
		removeAuthHeaders(connection)
	}
	if overrides.ModelConfig != nil || overrides.BaseURL != nil || overrides.API != nil || overrides.APIKeyEnv != nil {
		var entries []nativeObject
		if raw, ok := connection["models"]; ok && json.Unmarshal(raw, &entries) != nil {
			return "", "", configurationError("models.json.models", "invalid_config", "expected model definitions")
		}
		found := false
		for index, entry := range entries {
			if nativeString(entry, "id") != model {
				continue
			}
			found = true
			if overrides.ModelConfig != nil {
				entries[index] = buildModelDefinition(model, overrides.ModelConfig)
			} else {
				if overrides.BaseURL != nil {
					delete(entry, "baseUrl")
				}
				if overrides.API != nil {
					delete(entry, "api")
				}
				if overrides.APIKeyEnv != nil || overrides.BaseURL != nil {
					removeAuthHeaders(entry)
				}
			}
		}
		if !found && overrides.ModelConfig != nil {
			entries = append(entries, buildModelDefinition(model, overrides.ModelConfig))
		}
		if len(entries) > 0 {
			setNativeField(connection, "models", entries)
		}
		if raw, ok := connection["modelOverrides"]; ok {
			entries, err := decodeJSONObject(raw, "models.json.modelOverrides")
			if err != nil {
				return "", "", err
			}
			// Remove only fields that conflict with the explicit selection.
			if raw, ok := entries[model]; ok {
				entry, err := decodeJSONObject(raw, "models.json.modelOverrides.model")
				if err != nil {
					return "", "", err
				}
				if overrides.BaseURL != nil {
					delete(entry, "baseUrl")
				}
				if overrides.API != nil {
					delete(entry, "api")
				}
				if overrides.APIKeyEnv != nil || overrides.BaseURL != nil {
					removeAuthHeaders(entry)
				}
				if overrides.ModelConfig != nil {
					for _, key := range []string{"reasoning", "contextWindow", "maxTokens", "input", "cost", "name"} {
						delete(entry, key)
					}
				}
				setNativeField(entries, model, entry)
			}
			setNativeField(connection, "modelOverrides", entries)
		}
	}
	if len(connection) > 0 {
		setNativeField(providers, provider, connection)
		setNativeField(models, "providers", providers)
		profile["models.json"], _ = json.MarshalIndent(models, "", "  ")
	}
	return provider, model, nil
}
func removeAuthHeaders(target nativeObject) {
	var headers nativeObject
	if json.Unmarshal(target["headers"], &headers) != nil {
		return
	}
	for name := range headers {
		if strings.EqualFold(name, "authorization") || strings.EqualFold(name, "x-api-key") || strings.EqualFold(name, "api-key") {
			delete(headers, name)
		}
	}
	setNativeField(target, "headers", headers)
}
func buildModelDefinition(id string, model *ModelConfig) nativeObject {
	result := nativeObject{}
	setNativeField(result, "id", id)
	if model.Name != "" {
		setNativeField(result, "name", model.Name)
	}
	setNativeField(result, "reasoning", *model.Reasoning)
	setNativeField(result, "contextWindow", model.ContextWindow)
	setNativeField(result, "maxTokens", model.MaxTokens)
	if len(model.Input) > 0 {
		setNativeField(result, "input", model.Input)
	}
	if cost := model.Cost; cost != nil {
		setNativeField(result, "cost", map[string]float64{"input": cost.Input, "output": cost.Output, "cacheRead": cost.CacheRead, "cacheWrite": cost.CacheWrite})
	}
	return result
}
