package artifacts

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"controls/execution"
	"controls/internal/comms"
)

// Publish retains exact bytes and materializes a read-only local review copy.
// The database copy remains authoritative; chmod is not agent sandboxing.
func (h HostCollector) Publish(_ context.Context, work execution.Work, paths []string, id string) ([]comms.PublishedFile, error) {
	if h.Root == "" {
		return nil, fmt.Errorf("publication storage is not configured")
	}
	if len(paths) == 0 || len(paths) > 128 {
		return nil, fmt.Errorf("publish 1..128 explicit project files")
	}
	collected, err := Collect(work.Project, paths, nil)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(h.Root, "executions", work.ExecutionID, "publications", id)
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	published := make([]comms.PublishedFile, 0, len(collected))
	total := 0
	for _, artifact := range collected {
		file, err := os.Open(artifact.URI)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		total += len(body)
		if len(body) > 1024*1024 || total > 8*1024*1024 {
			return nil, fmt.Errorf("publication exceeds 1 MiB/file or 8 MiB total")
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		if digest != artifact.Digest {
			return nil, fmt.Errorf("%s changed during publication; retry after edits settle", artifact.Name)
		}
		target := filepath.Join(directory, artifact.Name)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(target, body, 0400); err != nil {
			return nil, err
		}
		artifact.URI = target
		published = append(published, comms.PublishedFile{Artifact: artifact, Content: body})
	}
	return published, nil
}
