// Package adapters defines the native-runtime preparation port. It has no
// scheduler, persistence, session-supervisor or native-runner dependencies.
package adapters

import (
	"context"
	"fmt"

	"controls/execution"
)

// Request contains pinned package data and invocation paths. Project remains
// the launch directory: workspace selection inside an entrypoint is preserved.
// MCP credentials are injected by the command runner, not serialized here.
type Request struct {
	Package       execution.PackageSnapshot
	Command       []string
	Project       string
	InvocationDir string
	FilesDir      string
	PromptPath    string
	InputsPath    string
	ConfigPath    string
	ExecutionID   string
	StepID        execution.StepID
	Visit         uint
	Try           uint
}

// Prepared augments the package launch without taking over process supervision.
// Environment is process-only and must never be copied into audit metadata.
// Files are owned by InvocationDir and retained with the host runtime files.
type Prepared struct {
	Command     []string
	Environment map[string]string
	InputMode   string
	Runtime     execution.RuntimeIdentity
}

// Adapter prepares one native invocation. Name and Capabilities must be static;
// they allow validation without starting a process or contacting a provider.
type Adapter interface {
	Name() string
	Capabilities() execution.RuntimeCapabilities
	Prepare(context.Context, Request) (Prepared, error)
}

// Registry is composed explicitly by the application. Adding a native runner
// requires an implementation and registration, not role-name/file discovery.
type Registry map[string]Adapter

func (r Registry) Select(spec *execution.NativeRuntime, concurrent bool) (Adapter, error) {
	name := "command"
	if spec != nil {
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		name = spec.Name
	}
	adapter := r[name]
	if adapter == nil {
		return nil, fmt.Errorf("unsupported runtime %q", name)
	}
	if adapter.Name() != name {
		return nil, fmt.Errorf("adapter registration %q has mismatched identity", name)
	}
	capabilities := adapter.Capabilities()
	if concurrent && (!capabilities.PeerMessages || !capabilities.IdleDelivery) {
		return nil, fmt.Errorf("runtime %q does not support concurrent peer delivery", name)
	}
	return adapter, nil
}

// Command preserves independently runnable deterministic/MCP packages. Native
// behavior inside an opaque script is not inferred or advertised by Controls.
type Command struct{}

func (Command) Name() string                                { return "command" }
func (Command) Capabilities() execution.RuntimeCapabilities { return execution.RuntimeCapabilities{} }
func (Command) Prepare(_ context.Context, request Request) (Prepared, error) {
	if request.Package.Runtime != nil && (request.Package.Runtime.Version != "" || request.Package.Runtime.Executable != "") {
		return Prepared{}, fmt.Errorf("runtime command does not accept version or executable settings")
	}
	return Prepared{Command: append([]string(nil), request.Command...), InputMode: "pty", Runtime: execution.RuntimeIdentity{Name: "command", AdapterVersion: "1", Capabilities: Command{}.Capabilities()}}, nil
}

// Validator rejects adapter-owned settings before the controller persists them.
// It must not start processes, contact providers, or resolve credential values.
type Validator interface {
	Validate(execution.PackageSnapshot) error
}

func (r Registry) Validate(packages []execution.PackageSnapshot) error {
	for _, pkg := range packages {
		adapter, err := r.Select(pkg.Runtime, false)
		if err != nil {
			return err
		}
		if validator, ok := adapter.(Validator); ok {
			if err := validator.Validate(pkg); err != nil {
				return fmt.Errorf("package %q: %w", pkg.Name, err)
			}
		}
	}
	return nil
}
