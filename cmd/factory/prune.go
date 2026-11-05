package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"controls/internal/localresources"
	"controls/internal/sessions"
)

func pruneCommand(ctx context.Context, args []string, root string, input io.Reader, output io.Writer) error {
	dryRun := false
	for _, arg := range args {
		if arg != "--dry-run" || dryRun {
			return errors.New("usage: factory prune [--dry-run]")
		}
		dryRun = true
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(output, "No managed resources to prune.")
		return nil
	} else if err != nil {
		return err
	}
	lock, err := lockStartup(ctx, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	lease, err := acquireServiceLease(root)
	if err != nil {
		return errors.New("Factory service is running; run factory stop --all")
	}
	defer lease.Close()
	// Older services may not hold a lease. A live listener is still authoritative.
	socket := sessions.SocketPath(root)
	socketInfo, socketErr := os.Lstat(socket)
	if socketErr != nil && !errors.Is(socketErr, os.ErrNotExist) {
		return socketErr
	}
	if socketErr == nil {
		if socketInfo.Mode()&os.ModeSocket == 0 {
			return errors.New("Factory socket path is not a socket")
		}
		connection, err := net.DialTimeout("unix", socket, 250*time.Millisecond)
		if err == nil {
			connection.Close()
			return errors.New("Factory service is running; run factory stop --all")
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
			return err
		}
	}
	registry := localresources.Registry{Root: root}
	plan, err := registry.PlanPrune(false)
	if err != nil {
		return err
	}
	for _, action := range plan {
		target := action.Path
		if target == "" {
			target = fmt.Sprintf("process record %d", action.Resource.Process.PID)
		}
		fmt.Fprintf(output, "remove %s: %s\n", action.Resource.Kind, target)
	}
	info, err := os.Lstat(socket)
	removeSocket := err == nil && info.Mode()&os.ModeSocket != 0
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if removeSocket {
		fmt.Fprintf(output, "remove stale socket: %s\n", socket)
	}
	if dryRun {
		return nil
	}
	if err := registry.ApplyPrune(plan); err != nil {
		return err
	}
	if removeSocket {
		return os.Remove(socket)
	}
	return nil
}
