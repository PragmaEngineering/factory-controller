package main

import (
	"context"
	"controls/internal/sessions"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
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
	var failures []error
	for _, kind := range []string{"monitor", "service", "agent"} {
		for _, item := range items {
			if item.Kind != kind {
				continue
			}
			if err := localresources.StopProcess(stopping, item.Process, kind == "agent" || kind == "monitor"); err != nil {
				failures = append(failures, fmt.Errorf("stop %s: %w", kind, err))
			}
		}
	}
	// Keep the database available when an agent could not be stopped. Still
	// attempt all recorded processes so one failure does not hide other results.
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	lease, err := acquireServiceLease(root)
	if err != nil {
		return errors.Join(gracefulErr, err)
	}
	defer lease.Close()
	// A pre-journal daemon can expose the socket without holding the new lease.
	// Never stop its database underneath a still-running, unidentified owner.
	if err := requireNoServiceListener(root); err != nil {
		return errors.Join(gracefulErr, err)
	}
	for _, item := range items {
		if item.Kind == "database" {
			if err := localresources.StopDatabase(stopping, item.Path); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func requireNoServiceListener(root string) error {
	socket := sessions.SocketPath(root)
	info, err := os.Lstat(socket)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("Factory socket path is not a socket")
	}
	connection, err := net.DialTimeout("unix", socket, 250*time.Millisecond)
	if err == nil {
		connection.Close()
		return errors.New("Factory service is still listening; no stopped owner was verified")
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	return fmt.Errorf("inspect Factory service listener: %w", err)
}
