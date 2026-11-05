package main

import (
	"context"
	"errors"
	"os"
	"time"

	"controls/internal/control"
	"controls/internal/localresources"
)

// Recovery is CLI-local and reads no execution database. It serializes against
// startup and only stops resources whose durable ownership can be verified.
func stopAllResources(ctx context.Context, root string, controller *control.Client) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	stopping, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// Prefer normal shutdown so execution/session state can be persisted.
	graceful, cancelGraceful := context.WithTimeout(stopping, 3*time.Second)
	gracefulErr := stopService(graceful, root, controller)
	cancelGraceful()
	lock, err := lockStartup(stopping, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	registry := localresources.Registry{Root: root}
	items, err := registry.List()
	if err != nil {
		return err
	}
	// A prior release has no resource journal. Adopt only the backend directory
	// derived from this exact runtime root, with executable/data verification.
	path, err := localresources.EmbeddedDatabasePath(root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := registry.RecordPath("database", path); err != nil {
			return err
		}
		items, err = registry.List()
		if err != nil {
			return err
		}
	}
	for _, kind := range []string{"service", "agent"} {
		for _, item := range items {
			if item.Kind != kind {
				continue
			}
			if err := localresources.StopProcess(stopping, item.Process, kind == "agent"); err != nil {
				return err
			}
		}
	}
	lease, err := acquireServiceLease(root)
	if err != nil {
		return errors.Join(gracefulErr, err)
	}
	defer lease.Close()
	for _, item := range items {
		if item.Kind == "database" {
			if err := localresources.StopDatabase(stopping, item.Path); err != nil {
				return err
			}
		}
	}
	return nil
}
