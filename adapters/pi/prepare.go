// Package pi prepares native Pi profiles. RPC messaging remains in the native
// JavaScript bridge; workflow scheduling never imports this package.
package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"controls/adapters"
	"controls/execution"
)

// Adapter handles the tested Pi 1.0.3 native profile contract.
// Expanding compatibility and configuration overrides remains CTRL-005 work.
type Adapter struct{}

func (Adapter) Name() string { return "pi" }
func (Adapter) Capabilities() execution.RuntimeCapabilities {
	return execution.RuntimeCapabilities{OperatorInput: true, PeerMessages: true, IdleDelivery: true, Lifecycle: true, Usage: true, ToolEvents: true}
}
func (Adapter) Prepare(ctx context.Context, request adapters.Request) (adapters.Prepared, error) {
	spec := request.Package.Runtime
	if spec == nil || spec.Name != "pi" {
		return adapters.Prepared{}, fmt.Errorf("pi preparation requires an explicit pi runtime")
	}
	executable := "pi"
	if spec.Executable != "" {
		executable = spec.Executable
	}
	path, version, err := adapters.ExecutableVersion(ctx, executable, spec.Version, "=1.0.3")
	if err != nil {
		return adapters.Prepared{}, fmt.Errorf("pi: %w", err)
	}
	// Reject settings we do not yet interpret, rather than silently ignoring a
	// factory's requested model/provider configuration.
	var settings struct {
		With map[string]json.RawMessage `json:"with"`
	}
	if err := json.Unmarshal(request.Package.Settings, &settings); err != nil {
		return adapters.Prepared{}, fmt.Errorf("pi package settings: %w", err)
	}
	if len(settings.With) != 0 {
		return adapters.Prepared{}, fmt.Errorf("pi runtime does not yet support with overrides (CTRL-005)")
	}
	profile := filepath.Join(request.InvocationDir, "profiles", "pi")
	if err := os.MkdirAll(profile, 0700); err != nil {
		return adapters.Prepared{}, err
	}
	files, err := filepath.Glob(filepath.Join(request.Package.Source, "pi", "*.json"))
	if err != nil {
		return adapters.Prepared{}, err
	}
	if len(files) == 0 {
		return adapters.Prepared{}, fmt.Errorf("pi package requires a native pi profile")
	}
	for _, source := range files {
		data, err := os.ReadFile(source)
		if err != nil {
			return adapters.Prepared{}, err
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || object == nil {
			return adapters.Prepared{}, fmt.Errorf("invalid pi profile %s", filepath.Base(source))
		}
		if err := os.WriteFile(filepath.Join(profile, filepath.Base(source)), data, 0600); err != nil {
			return adapters.Prepared{}, err
		}
	}
	return adapters.Prepared{Command: append([]string(nil), request.Command...), InputMode: "pty",
		Environment: map[string]string{"PI_CODING_AGENT_DIR": profile, "AGENT_EXECUTABLE": path},
		Runtime:     execution.RuntimeIdentity{Name: "pi", Version: version, AdapterVersion: "1", Executable: path, Capabilities: Adapter{}.Capabilities()},
	}, nil
}
