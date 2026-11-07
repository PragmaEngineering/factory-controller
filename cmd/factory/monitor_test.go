package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"controls/internal/control"
	"controls/internal/localresources"
	"controls/internal/sessions"

	"github.com/stretchr/testify/require"
)

func TestMonitorLaunchIsIndependentOfProjectAndService(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pragma-monitor"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nIFS= read -r line\nprintf 'input:%s\\n' \"$line\"\nprintf 'diagnostic\\n' >&2\n"), 0700))
	t.Setenv("PATH", bin)
	for _, invalidConfig := range []bool{false, true} {
		project := t.TempDir()
		t.Chdir(project)
		if invalidConfig {
			require.NoError(t, os.WriteFile(filepath.Join(project, "factory.json"), []byte("invalid json"), 0600))
		}
		root := filepath.Join(t.TempDir(), "absent-runtime")
		var output, diagnostics bytes.Buffer
		require.NoError(t, runCommand(context.Background(), []string{"monitor", "start", "--port", "4175"}, root, strings.NewReader("hello\n"), &output, &diagnostics))
		require.Equal(t, "--port\n4175\ninput:hello\n", output.String())
		require.Equal(t, "diagnostic\n", diagnostics.String())
		require.NoDirExists(t, root)
		require.NoDirExists(t, filepath.Join(project, ".factory"))
	}
}

func TestMonitorMissingInstallationIsActionable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	require.ErrorContains(t, runMonitor(context.Background(), []string{"start"}, t.TempDir(), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}), "Monitor is not installed on PATH")
}

func TestMonitorFailureIsReturned(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pragma-monitor"), []byte("#!/bin/sh\nexit 7\n"), 0700))
	require.ErrorContains(t, runMonitor(context.Background(), []string{"start"}, t.TempDir(), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}), "exit status 7")
}

func TestMonitorCancellationStopsChild(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pragma-monitor"), []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, runMonitor(ctx, []string{"start"}, t.TempDir(), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}))
}

func TestDetachedMonitorSurvivesCallerAndIsRecoverable(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "monitor-detach-")
	require.NoError(t, err)
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf 'args:%s\n' "$*"
if IFS= read -r line; then exit 9; fi
printf 'Pragma Monitor: http://127.0.0.1:4175\n'
exec /bin/sleep 60
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pragma-monitor"), []byte(script), 0700))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	require.NoError(t, runCommand(ctx, []string{"monitor", "start", "-d", "--port", "4175"}, root, strings.NewReader("must not reach child\n"), &output, &output))
	cancel()
	items, err := (localresources.Registry{Root: root}).List()
	require.NoError(t, err)
	var process *localresources.Process
	for _, item := range items {
		if item.Kind == "monitor" {
			process = item.Process
		}
		if item.Kind == "retained" {
			data, err := os.ReadFile(item.Path)
			require.NoError(t, err)
			require.Contains(t, string(data), "args:--port 4175")
			require.NotContains(t, string(data), "-d")
			info, err := os.Stat(item.Path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		}
	}
	require.NotNil(t, process)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = localresources.StopProcess(cleanup, process, true)
	})
	live, err := localresources.Matches(process)
	require.NoError(t, err)
	require.True(t, live)
	require.Contains(t, output.String(), "Pragma Monitor: http://127.0.0.1:4175\nPID ")
	require.Contains(t, output.String(), "\nLOG ")
	require.NoError(t, stopAllResources(context.Background(), root, control.NewClient(sessions.SocketPath(root))))
	live, err = localresources.Matches(process)
	require.NoError(t, err)
	require.False(t, live)
}

func TestDetachedMonitorReportsStartupFailure(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pragma-monitor"), []byte("#!/bin/sh\nprintf 'address already in use\\n' >&2\nexit 1\n"), 0700))
	var output bytes.Buffer
	err := runMonitor(context.Background(), []string{"start", "--detach"}, t.TempDir(), strings.NewReader(""), &output, &output)
	require.ErrorContains(t, err, "exited during startup")
	require.ErrorContains(t, err, "monitor log:")
	require.Empty(t, output.String())
}

func TestMonitorReadinessHasBoundedWait(t *testing.T) {
	log := filepath.Join(t.TempDir(), "monitor.log")
	require.NoError(t, os.WriteFile(log, nil, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := awaitMonitor(ctx, log, make(chan error))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestMonitorStopOnlyStopsManagedMonitorsWithoutUIExecutable(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	registry := localresources.Registry{Root: root}
	start := func(kind string) *exec.Cmd {
		command := exec.Command("/bin/sleep", "60")
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		require.NoError(t, command.Start())
		done := make(chan struct{})
		go func() { _ = command.Wait(); close(done) }()
		t.Cleanup(func() { _ = command.Process.Kill(); <-done })
		require.NoError(t, registry.RecordProcess(kind, command.Process.Pid))
		return command
	}
	monitor := start("monitor")
	service := start("service")
	agent := start("agent")
	t.Setenv("PATH", "/bin:/usr/bin") // No pragma-monitor installation.
	var output bytes.Buffer
	require.NoError(t, runCommand(context.Background(), []string{"monitor", "stop"}, root, strings.NewReader(""), &output, &output))
	process, err := localresources.Inspect(monitor.Process.Pid)
	require.NoError(t, err)
	require.Nil(t, process)
	for _, command := range []*exec.Cmd{service, agent} {
		process, err := localresources.Inspect(command.Process.Pid)
		require.NoError(t, err)
		require.NotNil(t, process)
	}
	require.NoError(t, runCommand(context.Background(), []string{"monitor", "stop"}, root, strings.NewReader(""), &output, &output))
}

func TestMonitorStopWithoutRuntimeDoesNotCreateIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	var output bytes.Buffer
	require.NoError(t, runCommand(context.Background(), []string{"monitor", "stop"}, root, strings.NewReader(""), &output, &output))
	require.NoDirExists(t, root)
}

func TestBareMonitorAndHelpNeedNoInstallationOrRuntime(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"monitor"}, {"monitor", "--help"}, {"monitor", "-h"}, {"monitor", "start", "--help"}, {"monitor", "start", "-d", "--help"}} {
		root := filepath.Join(t.TempDir(), "absent-runtime")
		var output bytes.Buffer
		require.NoError(t, runCommand(context.Background(), args, root, strings.NewReader(""), &output, &output))
		require.Contains(t, output.String(), "factory monitor start")
		require.Contains(t, output.String(), "factory monitor stop")
		require.NoDirExists(t, root)
	}
}

func TestMonitorRejectsImplicitLaunchAndUnknownCommands(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"monitor", "-d"}, {"monitor", "--port", "4175"}, {"monitor", "unknown"}} {
		var output bytes.Buffer
		err := runCommand(context.Background(), args, filepath.Join(t.TempDir(), "missing"), strings.NewReader(""), &output, &output)
		require.ErrorContains(t, err, "unknown monitor command")
		require.Empty(t, output.String())
	}
}
