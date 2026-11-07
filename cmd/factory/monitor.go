package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"controls/internal/localresources"
)

// Monitor remains a separate application. Resolve its installed entrypoint from
// PATH; neither project configuration nor a source-checkout layout selects it.
func runMonitor(ctx context.Context, args []string, root string, input io.Reader, output, diagnostics io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		monitorHelp(output)
		return nil
	}
	if len(args) > 0 && args[0] == "stop" {
		if len(args) != 1 {
			return errors.New("usage: factory monitor stop")
		}
		if err := stopMonitors(ctx, root); err != nil {
			return err
		}
		fmt.Fprintln(output, "Managed detached monitors are stopped.")
		return nil
	}
	if args[0] != "start" {
		return fmt.Errorf("unknown monitor command %q; use factory monitor for help", args[0])
	}
	args = args[1:]
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			monitorHelp(output)
			return nil
		}
	}
	detached := false
	var forwarded []string
	for _, arg := range args {
		if arg == "-d" || arg == "--detach" {
			if detached {
				return errors.New("duplicate monitor detach option")
			}
			detached = true
			continue
		}
		forwarded = append(forwarded, arg)
	}
	executable, err := exec.LookPath("pragma-monitor")
	if err != nil {
		return errors.New("Pragma Monitor is not installed on PATH; run npm ci, npm run build, and npm link in the Monitor checkout")
	}
	if detached {
		return startDetachedMonitor(ctx, executable, forwarded, root, output)
	}
	command := exec.CommandContext(ctx, executable, forwarded...)
	command.Stdin, command.Stdout, command.Stderr = input, output, diagnostics
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 3 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("run Pragma Monitor: %w", err)
	}
	return nil
}

// A detached monitor owns its session and file descriptors, so closing the
// invoking terminal does not close its output or send it a hangup signal.
func startDetachedMonitor(ctx context.Context, executable string, args []string, root string, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	lock, err := lockStartup(ctx, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	logFile, err := os.CreateTemp(root, "monitor-*.log")
	if err != nil {
		return err
	}
	defer logFile.Close()
	logPath, err := filepath.EvalSymlinks(logFile.Name())
	if err != nil {
		return err
	}
	resources := localresources.Registry{Root: root}
	if err := resources.RecordPath("retained", logPath); err != nil {
		return err
	}
	command := exec.Command(executable, args...)
	command.Dir = root
	command.Stdout, command.Stderr = logFile, logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Pragma Monitor: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	ready := false
	defer func() {
		if !ready {
			_ = command.Process.Signal(syscall.SIGTERM)
		}
	}()
	if err := resources.RecordProcess("monitor", command.Process.Pid); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url, err := awaitMonitor(startup, logPath, exited)
	if err != nil {
		return fmt.Errorf("%w; monitor log: %s", err, logPath)
	}
	ready = true
	fmt.Fprintf(output, "Pragma Monitor: %s\nPID %d\nLOG %s\n", url, command.Process.Pid, logPath)
	return nil
}

// The startup line is emitted by Monitor only after its listener binds. Read
// this launch's private log, never an arbitrary server already using the port.
func awaitMonitor(ctx context.Context, logPath string, exited <-chan error) (string, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("Monitor startup: %w", ctx.Err())
		case err := <-exited:
			if err == nil {
				return "", errors.New("Pragma Monitor exited before becoming ready")
			}
			return "", fmt.Errorf("Pragma Monitor exited during startup: %w", err)
		case <-ticker.C:
			file, err := os.Open(logPath)
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(io.LimitReader(file, 64<<10))
			file.Close()
			if err != nil {
				return "", err
			}
			for _, line := range strings.Split(string(data), "\n") {
				if url, ok := strings.CutPrefix(line, "Pragma Monitor: "); ok && strings.HasPrefix(url, "http://127.0.0.1:") {
					return url, nil
				}
			}
		}
	}
}

// Stopping a monitor uses the host ownership journal, not the daemon or the
// installed UI executable. Other managed resource kinds are never stopped here.
func stopMonitors(ctx context.Context, root string) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	stopping, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lock, err := lockStartup(stopping, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	items, err := (localresources.Registry{Root: root}).List()
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range items {
		if item.Kind != "monitor" {
			continue
		}
		if err := localresources.StopProcess(stopping, item.Process, true); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func monitorHelp(output io.Writer) {
	fmt.Fprint(output, `Pragma Monitor

  factory monitor start [-d | --detach] [--port PORT] [--socket PATH]
  factory monitor stop

Start in the foreground, or detach and print the URL, PID, and log path.
Stop all Factory-managed detached monitors without stopping the execution service.
Monitor must be installed separately; no factory.json is required.
`)
}
