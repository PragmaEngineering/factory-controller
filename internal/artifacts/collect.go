package artifacts

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"controls/execution"
)

// The workflow owns required output names. Agents only need to create the files;
// Factory records their paths and digests after the result is submitted.
func Collect(project string, required []string, reported []execution.Artifact) ([]execution.Artifact, error) {
	artifacts := make([]execution.Artifact, 0, len(reported)+len(required))
	for _, artifact := range reported {
		keep := true
		for _, name := range required {
			if artifact.Name == name {
				keep = false
				break
			}
		}
		if keep {
			artifacts = append(artifacts, artifact)
		}
	}
	root, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	for _, name := range required {
		if !filepath.IsLocal(name) {
			return nil, fmt.Errorf("required output %q must be a project-relative path", name)
		}
		path, err := filepath.EvalSymlinks(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("required output %q: %w", name, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, fmt.Errorf("required output %q resolves outside the project", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("required output %q: %w", name, err)
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			return nil, fmt.Errorf("required output %q is not a regular file", name)
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		closeErr := file.Close()
		if err != nil {
			return nil, fmt.Errorf("hash required output %q: %w", name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close required output %q: %w", name, closeErr)
		}
		artifacts = append(artifacts, execution.Artifact{Name: name, URI: path, Digest: fmt.Sprintf("%x", hash.Sum(nil))})
	}
	return artifacts, nil
}
