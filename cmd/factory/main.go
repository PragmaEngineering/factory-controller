package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"controls/internal/app"
	"controls/internal/cli"
	"controls/internal/control"
	"controls/internal/sessions"
)

func main() {
	if err := run(); err != nil {
		if len(os.Args) < 2 || os.Args[1] != "_serve" {
			err = cli.PublicError(err)
		}
		fmt.Fprintln(os.Stderr, "factory:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := sessions.RuntimeRoot()
	if err != nil {
		return err
	}
	if len(os.Args) == 2 && os.Args[1] == "_serve" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return app.Serve(ctx, root)
	}
	if len(os.Args) == 1 || os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		cli.Help(os.Stdout)
		return nil
	}
	controller := control.NewClient(sessions.SocketPath(root))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ensureService(ctx, root, controller); err != nil {
		return err
	}
	return cli.Run(ctx, os.Args[1:], controller, sessions.NewClient(sessions.SocketPath(root)), os.Stdin, os.Stdout)
}

func ensureService(ctx context.Context, root string, controller *control.Client) error {
	ready, err := serviceReady(ctx, root, controller)
	if ready || err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	fmt.Fprintln(os.Stderr, "Factory: connecting to local execution service (Ctrl-C to cancel)")
	lock, err := lockStartup(startup, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Another client may have started the service while this one waited.
	ready, err = serviceReady(startup, root, controller)
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
	fmt.Fprintln(os.Stderr, "Factory: starting local execution service (Ctrl-C to cancel)")
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Factory service: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	if err := awaitService(startup, controller.Version, exited, os.Stderr); err != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
		return fmt.Errorf("%w; service log: %s", err, filepath.Join(root, "service.log"))
	}
	return nil
}

func serviceReady(ctx context.Context, root string, controller *control.Client) (bool, error) {
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
