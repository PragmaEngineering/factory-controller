package adapters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// ExecutableVersion resolves once, then checks the exact program that will run.
// A runner-specific adapter supplies its known version flag and supported range.
func ExecutableVersion(ctx context.Context, executable, required, supported string) (string, string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", "", fmt.Errorf("runtime executable %q unavailable", executable)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve runtime executable: %w", err)
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(probe, path, "--version")
	command.Env = []string{}
	// A version probe needs executable lookup/home/temp, not provider or engine
	// credentials from the control service. This is not host filesystem isolation.
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	output, err := command.Output()
	if err != nil {
		return "", "", fmt.Errorf("runtime version probe failed for %q", path)
	}
	// Only a bare semantic version is accepted. Native banners need an explicit
	// adapter parser, rather than guessing from arbitrary program output.
	version, err := semver.StrictNewVersion(strings.TrimSpace(string(output)))
	if err != nil {
		return "", "", fmt.Errorf("runtime %q returned an invalid semantic version", path)
	}
	for _, constraint := range []string{supported, required} {
		if constraint == "" {
			continue
		}
		allowed, err := semver.NewConstraint(constraint)
		if err != nil {
			return "", "", fmt.Errorf("invalid runtime compatibility constraint")
		}
		if !allowed.Check(version) {
			return "", "", fmt.Errorf("runtime version %s does not satisfy %q", version, constraint)
		}
	}
	return path, version.String(), nil
}
