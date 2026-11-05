package execution

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// NativeRuntime identifies a native program, not the host process mechanism.
// An omitted runtime retains an opaque command package with no agent capabilities.
type NativeRuntime struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Executable string `json:"executable,omitempty"`
}

func (r *NativeRuntime) Validate() error {
	if r == nil {
		return nil
	}
	if r.Name == "" || strings.ContainsAny(r.Name, "/\\ \t\n") {
		return fmt.Errorf("runtime.name must be a nonempty adapter name")
	}
	if r.Version != "" {
		if _, err := semver.NewConstraint(r.Version); err != nil {
			return fmt.Errorf("runtime.version: invalid compatibility constraint")
		}
	}
	return nil
}

// RuntimeCapabilities describe an adapter's implemented behavior, not a grant.
// They do not establish filesystem, network or credential isolation.
type RuntimeCapabilities struct {
	OperatorInput bool `json:"operator_input"`
	PeerMessages  bool `json:"peer_messages"`
	IdleDelivery  bool `json:"idle_delivery"`
	Lifecycle     bool `json:"lifecycle"`
	Usage         bool `json:"usage"`
	ToolEvents    bool `json:"tool_events"`
}

// RuntimeIdentity contains only safe adapter metadata, never effective secrets.
type RuntimeIdentity struct {
	Name           string              `json:"name"`
	Version        string              `json:"version,omitempty"`
	AdapterVersion string              `json:"adapter_version"`
	Executable     string              `json:"executable,omitempty"`
	Capabilities   RuntimeCapabilities `json:"capabilities"`
	// Configuration is adapter-owned audit metadata; it must contain no credential values.
	Configuration map[string]json.RawMessage `json:"configuration,omitempty"`
}
