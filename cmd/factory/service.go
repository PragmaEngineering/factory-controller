package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"controls/internal/control"
	"controls/internal/sessions"
)

func startService(ctx context.Context, root string, controller *control.Client, output io.Writer) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	lock, err := lockStartup(startup, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Another client may have started the service while this one waited.
	ready, err := serviceReady(startup, root, controller)
	if ready || err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(root, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	command := exec.Command(executable, "_serve")
	command.Dir = root
	command.Stdout, command.Stderr = logFile, logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	fmt.Fprintln(output, "Factory: starting local execution service (Ctrl-C to cancel)")
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Factory service: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	if err := awaitService(startup, controller.Version, exited, output); err != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
		return fmt.Errorf("%w; service log: %s", err, filepath.Join(root, "service.log"))
	}
	return nil
}

func serviceReady(ctx context.Context, root string, controller *control.Client) (bool, error) {
	if _, err := os.Lstat(sessions.SocketPath(root)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect Factory service socket: %w", err)
	}
	probe, cancel := context.WithTimeout(ctx, time.Second)
	err := sessions.NewClient(sessions.SocketPath(root)).Health(probe)
	cancel()
	if err == nil {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := controller.Version(probe); err != nil {
			return false, fmt.Errorf("an incompatible Factory service is running: %w", err)
		}
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	// A live but unresponsive listener must not trigger a second database owner.
	probe, cancel = context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	connection, dialErr := (&net.Dialer{}).DialContext(probe, "unix", sessions.SocketPath(root))
	if dialErr == nil {
		connection.Close()
		return false, fmt.Errorf("the local Factory service is not responding: %w", err)
	}
	if !errors.Is(dialErr, os.ErrNotExist) && !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return false, fmt.Errorf("connect to Factory service: %w", dialErr)
	}
	return false, nil
}

// An OS lock coordinates separate CLI processes and is released if its owner
// exits. Never remove the lock file: other clients may already have it open.
func lockStartup(ctx context.Context, root string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(root, "startup.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// awaitService observes the process as well as the API: a failed service must
// not leave the CLI polling a socket for the entire startup timeout.
func awaitService(ctx context.Context, ready func(context.Context) error, exited <-chan error, output io.Writer) error {
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	progress := time.NewTicker(5 * time.Second)
	defer progress.Stop()
	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("Factory startup cancelled: %w", ctx.Err())
			}
			return fmt.Errorf("Factory service did not become ready: %w", ctx.Err())
		case err := <-exited:
			if err != nil {
				return fmt.Errorf("Factory service exited during startup: %w", err)
			}
			return errors.New("Factory service exited before becoming ready")
		case <-progress.C:
			fmt.Fprintf(output, "Factory: waiting for local service (%s elapsed; Ctrl-C to cancel)\n", time.Since(started).Round(time.Second))
		case <-poll.C:
			probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := ready(probe)
			cancel()
			if err == nil {
				return nil
			}
		}
	}
}

func requireService(ctx context.Context, root string, controller *control.Client) error {
	ready, err := serviceReady(ctx, root, controller)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("Factory service is not running. Run factory start.")
	}
	return nil
}

// The lease is held until all service cleanup finishes, including database
// shutdown. Stop waits for it rather than mistaking socket closure for exit.
func acquireServiceLease(root string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(root, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("Factory service is already running or shutting down: %w", err)
	}
	return file, nil
}

func stopService(ctx context.Context, root string, controller *control.Client) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	stopping, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	lock, err := lockStartup(stopping, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	connection, err := (&net.Dialer{}).DialContext(stopping, "unix", sessions.SocketPath(root))
	if err == nil {
		connection.Close()
		if err := controller.Shutdown(stopping); err != nil {
			return fmt.Errorf("stop Factory service: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("connect to Factory service: %w", err)
	}
	// A missing socket can also mean shutdown is still releasing the database.

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		lease, err := acquireServiceLease(root)
		if err == nil {
			lease.Close()
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-stopping.Done():
			return fmt.Errorf("wait for Factory service shutdown: %w", stopping.Err())
		case <-ticker.C:
		}
	}
}
