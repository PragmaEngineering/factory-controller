package factoryconfig

import (
	"fmt"
	"path/filepath"
	"strings"
)

// factory_dir is a literal path relative to factory.json, not a shell expression.
func executionDirectories(configured, project, id string) (string, string, error) {
	if configured == "" {
		configured = ".factory"
	}
	if strings.ContainsRune(configured, 0) || strings.Contains(configured, "${") {
		return "", "", fmt.Errorf("factory_dir must be a literal filesystem path")
	}
	if !filepath.IsAbs(configured) {
		configured = filepath.Join(project, configured)
	}
	directory := filepath.Clean(configured)
	if id != "" && (!filepath.IsLocal(id) || filepath.Base(id) != id || id == ".") {
		return "", "", fmt.Errorf("execution ID must be a single path component")
	}
	if id == "" {
		id = "${execution.id}"
	}
	return directory, filepath.Join(directory, "executions", id, "files"), nil
}
