// Package conformance is an executable reference adapter for integration tests.
// It is intentionally not registered in the production application.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"controls/adapters"
	"controls/execution"
)

type Adapter struct{}

func (Adapter) Name() string { return "fixture" }
func (Adapter) Capabilities() execution.RuntimeCapabilities {
	return execution.RuntimeCapabilities{PeerMessages: true, IdleDelivery: true}
}
func (Adapter) Prepare(ctx context.Context, request adapters.Request) (adapters.Prepared, error) {
	spec := request.Package.Runtime
	if spec == nil || spec.Name != "fixture" {
		return adapters.Prepared{}, fmt.Errorf("fixture preparation requires an explicit fixture runtime")
	}
	path, version, err := adapters.ExecutableVersion(ctx, request.Command[0], spec.Version, "=1.0.0")
	if err != nil {
		return adapters.Prepared{}, err
	}
	if spec.Executable != "" {
		return adapters.Prepared{}, fmt.Errorf("fixture uses its package entrypoint as executable")
	}
	var settings struct {
		With json.RawMessage `json:"with"`
	}
	if err := json.Unmarshal(request.Package.Settings, &settings); err != nil {
		return adapters.Prepared{}, err
	}
	if len(settings.With) == 0 {
		settings.With = json.RawMessage(`{}`)
	}
	config := filepath.Join(request.InvocationDir, "fixture-config.json")
	if err := os.WriteFile(config, settings.With, 0600); err != nil {
		return adapters.Prepared{}, err
	}
	command := append([]string{path}, request.Command[1:]...)
	return adapters.Prepared{Command: command, InputMode: "none", Environment: map[string]string{"FIXTURE_CONFIG": config}, Runtime: execution.RuntimeIdentity{Name: "fixture", Version: version, Executable: path, AdapterVersion: "1", Capabilities: Adapter{}.Capabilities()}}, nil
}
