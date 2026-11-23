package pi

import (
	"context"
	"controls/adapters"
	"controls/execution"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This subprocess reads the profile and CLI arguments rather than trusting the
// requested factory JSON. No actual credential is returned in its responses.
func TestPiNativeProcess(t *testing.T) {
	if os.Getenv("TEST_NATIVE_PI") != "1" {
		return
	}
	profile := os.Getenv("PI_CODING_AGENT_DIR")
	var settings map[string]any
	data, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	if err != nil {
		os.Exit(2)
	}
	_ = json.Unmarshal(data, &settings)
	provider, _ := settings["defaultProvider"].(string)
	model, _ := settings["defaultModel"].(string)
	thinking, _ := settings["defaultThinkingLevel"].(string)
	if thinking == "" {
		thinking = "off"
	}
	for i := 0; i+1 < len(os.Args); i++ {
		switch os.Args[i] {
		case "--provider":
			provider = os.Args[i+1]
		case "--model":
			model = os.Args[i+1]
		case "--thinking":
			thinking = os.Args[i+1]
		}
	}
	var catalog struct {
		Providers map[string]struct {
			API     string            `json:"api"`
			BaseURL string            `json:"baseUrl"`
			APIKey  string            `json:"apiKey"`
			Models  []nativeModelInfo `json:"models"`
		} `json:"providers"`
	}
	data, _ = os.ReadFile(filepath.Join(profile, "models.json"))
	_ = json.Unmarshal(data, &catalog)
	connection := catalog.Providers[provider]
	selected := nativeModelInfo{ID: model, Provider: provider, API: connection.API, BaseURL: connection.BaseURL}
	for _, candidate := range connection.Models {
		if candidate.ID == model {
			selected.Reasoning = candidate.Reasoning
			selected.ContextWindow = candidate.ContextWindow
			selected.MaxTokens = candidate.MaxTokens
		}
	}
	if !selected.Reasoning {
		thinking = "off"
	}
	state := map[string]any{"model": selected, "thinkingLevel": thinking}
	if os.Getenv("FACTORY_TEST_CAPTURE") != "" {
		captured := map[string]any{"state": state, "profile": profile, "credential_present": os.Getenv(strings.TrimPrefix(connection.APIKey, "$")) != ""}
		output, _ := json.Marshal(captured)
		_ = os.WriteFile(os.Getenv("FACTORY_TEST_CAPTURE"), output, 0600)
		os.Exit(0)
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if decoder.Decode(&request) != nil {
			os.Exit(0)
		}
		result := any(state)
		if request.Type == "get_available_models" {
			result = map[string]any{"models": []nativeModelInfo{selected}}
		}
		if os.Getenv("TEST_PREFLIGHT_FAILURE") == "1" {
			fmt.Fprintln(os.Stderr, "recognizable-secret-value")
			os.Exit(1)
		}
		_ = encoder.Encode(map[string]any{"id": request.ID, "success": true, "data": result})
	}
}
func nativeFixture(t *testing.T) adapters.Request {
	t.Helper()
	t.Setenv("TEST_NATIVE_PI", "1")
	t.Setenv("CORPORATE_TEST_KEY", "recognizable-secret-value")
	source := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(source, "pi"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "pi", "settings.json"), []byte(`{"defaultProvider":"original","defaultModel":"original-model","defaultThinkingLevel":"medium","defaultTools":["read"],"packages":["retained-package"]}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "pi", "models.json"), []byte(`{"providers":{"original":{"api":"openai-completions","baseUrl":"https://original.example/v1","apiKey":"$ORIGINAL_KEY","models":[{"id":"original-model","reasoning":true,"contextWindow":10000,"maxTokens":1000}]}}}`), 0600))
	executable := filepath.Join(t.TempDir(), "pi")
	testBinary, err := os.Executable()
	require.NoError(t, err)
	script := "#!/bin/sh\nif [ \"${1:-}\" = --version ]; then echo 1.0.3; exit 0; fi\nexec '" + strings.ReplaceAll(testBinary, "'", "'\"'\"'") + "' -test.run='^TestPiNativeProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(executable, []byte(script), 0700))
	return adapters.Request{Package: execution.PackageSnapshot{Source: source, Settings: json.RawMessage(`{}`), Runtime: &execution.NativeRuntime{Name: "pi", Executable: executable, Version: "=1.0.3"}}, Command: []string{"launcher", "-p", "assignment"}, InvocationDir: t.TempDir()}
}
func customOverrides() json.RawMessage {
	return json.RawMessage(`{"overrides":{"provider":"corporate","model":"coding-v1","base_url":"https://corporate.example/v1","api":"openai-completions","api_key_env":"CORPORATE_TEST_KEY","thinking":"medium","model_config":{"reasoning":true,"context_window":32000,"max_tokens":4000,"input":["text"]}}}`)
}
func TestCustomConnectionUsesPrivateProfileAndSafeMetadata(t *testing.T) {
	request := nativeFixture(t)
	request.Package.Settings = customOverrides()
	before, err := os.ReadFile(filepath.Join(request.Package.Source, "pi", "settings.json"))
	require.NoError(t, err)
	prepared, err := (Adapter{}).Prepare(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "recognizable-secret-value", prepared.Environment["CORPORATE_TEST_KEY"])
	require.Contains(t, prepared.Command, "--provider")
	require.Contains(t, prepared.Command, "corporate")
	profile := prepared.Environment["PI_CODING_AGENT_DIR"]
	raw, err := os.ReadFile(filepath.Join(profile, "models.json"))
	require.NoError(t, err)
	var models map[string]any
	require.NoError(t, json.Unmarshal(raw, &models))
	providers := models["providers"].(map[string]any)
	require.Contains(t, providers, "original")
	corporate := providers["corporate"].(map[string]any)
	require.Equal(t, "$CORPORATE_TEST_KEY", corporate["apiKey"])
	require.NotContains(t, string(raw), "recognizable-secret-value")
	data, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), "retained-package")
	require.Contains(t, string(data), "defaultTools")
	after, err := os.ReadFile(filepath.Join(request.Package.Source, "pi", "settings.json"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	audit, err := json.Marshal(prepared.Runtime)
	require.NoError(t, err)
	require.Contains(t, string(audit), "corporate")
	require.NotContains(t, string(audit), "recognizable-secret-value")
}
func TestOverriddenCredentialsCannotUseStoredAuth(t *testing.T) {
	request := nativeFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(request.Package.Source, "pi", "auth.json"), []byte(`{"original":{"type":"api_key","key":"old-auth"},"other":{"type":"api_key","key":"keep-auth"}}`), 0600))
	request.Package.Settings = json.RawMessage(`{"overrides":{"api_key_env":"CORPORATE_TEST_KEY"}}`)
	prepared, err := (Adapter{}).Prepare(context.Background(), request)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(prepared.Environment["PI_CODING_AGENT_DIR"], "auth.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "old-auth")
	require.Contains(t, string(data), "keep-auth")
}
func TestOverrideValidationAndCapabilityErrors(t *testing.T) {
	request := nativeFixture(t)
	for _, text := range []string{
		`{"overrides":null}`, `{"overrides":{"model":""}}`, `{"overrides":{"model":null}}`,
		`{"overrides":{"api_key_env":"$BAD"}}`, `{"overrides":{"api_key_env":"PI_CODING_AGENT_DIR"}}`, `{"overrides":{"api_key_env":"FACTORY_MCP_TOKEN"}}`, `{"with":{"model":"ignored"}}`, `{"overrides":{"api":"invented"}}`,
		`{"overrides":{"base_url":"https://host/?key=secret"}}`, `{"overrides":{"thinking":"sometimes"}}`,
		`{"overrides":{"model_config":{"reasoning":true,"context_window":10,"max_tokens":100}}}`,
	} {
		require.Error(t, (Adapter{}).Validate(execution.PackageSnapshot{Settings: json.RawMessage(text)}), text)
	}
	for _, test := range []struct{ settings, field string }{
		{`{"overrides":{"provider":"different"}}`, "overrides.model"},
		{`{"overrides":{"provider":"custom","model":"x","base_url":"https://host/v1"}}`, "overrides"},
		{`{"overrides":{"api_key_env":"NONEXISTENT_FACTORY_TEST_CREDENTIAL"}}`, "api_key_env"},
	} {
		request.Package.Settings = json.RawMessage(test.settings)
		request.InvocationDir = t.TempDir()
		_, err := (Adapter{}).Prepare(context.Background(), request)
		require.ErrorContains(t, err, test.field)
	}
	request.Package.Settings = json.RawMessage(strings.Replace(string(customOverrides()), `"reasoning":true`, `"reasoning":false`, 1))
	_, err := (Adapter{}).Prepare(context.Background(), request)
	require.ErrorContains(t, err, "unsupported_capability")
}
func TestNativeFlagsCannotUndoOverrides(t *testing.T) {
	for _, args := range [][]string{{"--model", "other"}, {"--api-key=secret"}, {"--resume"}, {"--models", "pattern"}, {"--provider", "other"}} {
		require.Error(t, validateArguments(args))
	}
	require.NoError(t, validateArguments([]string{"-p", "a prompt containing --model and --api-key", "--workspace", "somewhere"}))
}
func TestPreflightErrorDoesNotExposeNativeSecrets(t *testing.T) {
	request := nativeFixture(t)
	request.Package.Settings = customOverrides()
	t.Setenv("TEST_PREFLIGHT_FAILURE", "1")
	_, err := (Adapter{}).Prepare(context.Background(), request)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "recognizable-secret-value")
}
func TestInstalledPi1xResolvesOverridesWithoutProviderRequests(t *testing.T) {
	executable := os.Getenv("FACTORY_TEST_INSTALLED_PI")
	if executable == "" {
		t.Skip("set FACTORY_TEST_INSTALLED_PI for the installed Pi 1.x smoke check")
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	request := nativeFixture(t)
	request.Package.Runtime.Executable = executable
	request.Package.Runtime.Version = supportedVersions
	request.Package.Settings = json.RawMessage(strings.Replace(string(customOverrides()), "https://corporate.example/v1", server.URL+"/v1", 1))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	prepared, err := (Adapter{}).Prepare(ctx, request)
	require.NoError(t, err)
	require.JSONEq(t, `"corporate"`, string(prepared.Runtime.Configuration["provider"]))
	require.JSONEq(t, `"coding-v1"`, string(prepared.Runtime.Configuration["model"]))
	require.Zero(t, calls.Load(), "configuration preflight must not make model requests")
}

func TestPreparedLaunchUsesExactNativeConfiguration(t *testing.T) {
	request := nativeFixture(t)
	launcher := filepath.Join(t.TempDir(), "start.sh")
	require.NoError(t, os.WriteFile(launcher, []byte("#!/bin/sh\nexec \"$AGENT_EXECUTABLE\" \"$@\"\n"), 0700))
	request.Command[0] = launcher
	request.Package.Settings = customOverrides()
	prepared, err := (Adapter{}).Prepare(context.Background(), request)
	require.NoError(t, err)
	capture := filepath.Join(t.TempDir(), "capture.json")
	command := exec.Command(prepared.Command[0], prepared.Command[1:]...)
	command.Env = append(os.Environ(), "FACTORY_TEST_CAPTURE="+capture)
	for name, value := range prepared.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	var actual struct {
		State struct {
			Model    nativeModelInfo `json:"model"`
			Thinking string          `json:"thinkingLevel"`
		} `json:"state"`
		Profile    string `json:"profile"`
		Credential bool   `json:"credential_present"`
	}
	require.NoError(t, json.Unmarshal(data, &actual))
	require.Equal(t, "corporate", actual.State.Model.Provider)
	require.Equal(t, "coding-v1", actual.State.Model.ID)
	require.Equal(t, "medium", actual.State.Thinking)
	require.Equal(t, prepared.Environment["PI_CODING_AGENT_DIR"], actual.Profile)
	require.True(t, actual.Credential)
	require.NotContains(t, string(data), "recognizable-secret-value")
}

func TestConcurrentPreparationsDoNotShareProfiles(t *testing.T) {
	request := nativeFixture(t)
	request.Package.Settings = customOverrides()
	type answer struct {
		prepared adapters.Prepared
		err      error
	}
	answers := make(chan answer, 2)
	for _, model := range []string{"coding-v1", "review-v2"} {
		current := request
		current.Package.Settings = json.RawMessage(strings.ReplaceAll(string(customOverrides()), "coding-v1", model))
		go func() {
			prepared, err := (Adapter{}).Prepare(context.Background(), current)
			answers <- answer{prepared, err}
		}()
	}
	first, second := <-answers, <-answers
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.NotEqual(t, first.prepared.Environment["PI_CODING_AGENT_DIR"], second.prepared.Environment["PI_CODING_AGENT_DIR"])
	require.NotEqual(t, string(first.prepared.Runtime.Configuration["model"]), string(second.prepared.Runtime.Configuration["model"]))
}

func TestSelectedOverridesPreserveUnrelatedNativeSettings(t *testing.T) {
	profile := map[string][]byte{
		"settings.json": []byte(`{"defaultProvider":"original","defaultModel":"one","theme":"unchanged"}`),
		"models.json":   []byte(`{"providers":{"original":{"api":"openai-completions","baseUrl":"https://old.example/v1","headers":{"Authorization":"old-key","X-Organization":"keep-org"},"models":[{"id":"one","baseUrl":"https://model.example/v1","reasoning":true,"contextWindow":10000,"maxTokens":1000,"headers":{"Authorization":"old-key","X-Trace":"keep-trace"}}],"modelOverrides":{"one":{"baseUrl":"https://override.example/v1","reasoning":true,"contextWindow":20000,"headers":{"Authorization":"old-key","X-Feature":"keep-feature"}},"two":{"name":"keep-model"}}},"unrelated":{"apiKey":"$OTHER_KEY"}}}`),
	}
	overrides, _, err := decodeOverrides(json.RawMessage(`{"overrides":{"base_url":"https://new.example/v1","api_key_env":"MY_KEY"}}`))
	require.NoError(t, err)
	_, _, err = applyProfileOverrides(profile, overrides)
	require.NoError(t, err)
	raw := string(profile["models.json"])
	require.NotContains(t, raw, "old-key")
	require.NotContains(t, raw, "https://override.example")
	require.NotContains(t, raw, "https://model.example")
	for _, value := range []string{"keep-org", "keep-trace", "keep-feature", "keep-model", "$OTHER_KEY", "20000"} {
		require.Contains(t, raw, value)
	}
}
