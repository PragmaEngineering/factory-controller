package main

import (
	"bytes"
	"context"
	"controls/internal/localresources"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"controls/internal/control"
	"controls/internal/sessions"

	"github.com/stretchr/testify/require"
)

func fakeService(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "factory-service-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(root) })
	listener, err := net.Listen("unix", sessions.SocketPath(root))
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return root
}

func TestOperationalCommandsDoNotStartService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent-runtime")
	for _, args := range [][]string{{"ls"}, {"inbox"}, {"exec", "brief"}, {"mcp"}, {"an-execution"}} {
		var output bytes.Buffer
		err := runCommand(context.Background(), args, root, strings.NewReader(""), &output, &output)
		require.ErrorContains(t, err, "Factory service is not running. Run factory start.")
		require.Empty(t, output.String())
		require.NoDirExists(t, root)
	}
}
func TestStartReusesRunningService(t *testing.T) {
	root := fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			io.WriteString(w, `{"ready":true}`)
			return
		}
		if r.URL.Path == "/version" {
			io.WriteString(w, `{"api":"factory-control-v3"}`)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(404)
	})
	var output bytes.Buffer
	require.NoError(t, runCommand(context.Background(), []string{"start"}, root, strings.NewReader(""), &output, &output))
	require.Equal(t, "Factory service is running.\n", output.String())
	require.NoFileExists(t, filepath.Join(root, "service.log"))
}
func TestStopAbsentServiceDoesNotCreateRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	require.NoError(t, stopService(context.Background(), root, control.NewClient(sessions.SocketPath(root))))
	require.NoDirExists(t, root)
}
func TestStopWaitsForServiceLease(t *testing.T) {
	requested := make(chan struct{})
	root := fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/shutdown", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusAccepted)
		close(requested)
	})
	lease, err := acquireServiceLease(root)
	require.NoError(t, err)
	defer lease.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- stopService(ctx, root, control.NewClient(sessions.SocketPath(root))) }()
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal("shutdown was not requested")
	}
	select {
	case err := <-done:
		t.Fatalf("stop returned before service cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, lease.Close())
	require.NoError(t, <-done)
}
func TestServiceConnectionTimeoutDoesNotStartAnotherProcess(t *testing.T) {
	root := fakeService(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require.Error(t, requireService(ctx, root, control.NewClient(sessions.SocketPath(root))))
	require.NoFileExists(t, filepath.Join(root, "service.log"))
}
func TestLifecycleCommandsRejectArguments(t *testing.T) {
	for _, name := range []string{"start", "stop"} {
		err := runCommand(context.Background(), []string{name, "extra"}, t.TempDir(), strings.NewReader(""), io.Discard, io.Discard)
		require.ErrorContains(t, err, "usage: factory "+name)
	}
}

func TestDaemonLifecycleIgnoresProjectConfiguration(t *testing.T) {
	requests := make(chan string, 12)
	root := fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		switch r.URL.Path {
		case "/health":
			io.WriteString(w, `{"ready":true}`)
		case "/version":
			io.WriteString(w, `{"api":"factory-control-v3"}`)
		case "/shutdown":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	for _, withInvalidConfig := range []bool{false, true} {
		directory := t.TempDir()
		if withInvalidConfig {
			require.NoError(t, os.WriteFile(filepath.Join(directory, "factory.json"), []byte("invalid project configuration"), 0600))
		}
		t.Chdir(directory)
		for _, command := range []string{"start", "stop"} {
			var output bytes.Buffer
			require.NoError(t, runCommand(context.Background(), []string{command}, root, strings.NewReader(""), &output, &output))
		}
		require.NoDirExists(t, filepath.Join(directory, ".factory"))
		for _, route := range []string{"/health", "/version", "/shutdown"} {
			require.Equal(t, route, <-requests)
		}
	}
	require.Empty(t, requests)
}

func TestStopAllAbsentServiceDoesNotCreateRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	var output bytes.Buffer
	require.NoError(t, runCommand(context.Background(), []string{"stop", "--all"}, root, strings.NewReader(""), &output, &output))
	require.NoDirExists(t, root)
}

func TestStopAllRecoversOrphanAgent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	command := exec.Command("sleep", "60")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, command.Start())
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-done })
	registry := localresources.Registry{Root: root}
	require.NoError(t, registry.RecordProcess("agent", command.Process.Pid))
	require.NoError(t, os.WriteFile(filepath.Join(root, "evidence"), []byte("retained"), 0600))
	require.NoError(t, stopAllResources(context.Background(), root, control.NewClient(sessions.SocketPath(root))))
	process, err := localresources.Inspect(command.Process.Pid)
	require.NoError(t, err)
	require.Nil(t, process)
	require.FileExists(t, filepath.Join(root, "evidence"))
}

func TestPruneDryRunAndOfflineCleanup(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	profiles := filepath.Join(root, "profiles")
	require.NoError(t, os.Mkdir(profiles, 0700))
	require.NoError(t, (localresources.Registry{Root: root}).RecordPath("disposable", profiles))
	var output bytes.Buffer
	require.NoError(t, runCommand(context.Background(), []string{"prune", "--dry-run"}, root, strings.NewReader(""), &output, &output))
	require.Contains(t, output.String(), profiles)
	require.DirExists(t, profiles)
	require.NoError(t, runCommand(context.Background(), []string{"prune"}, root, strings.NewReader(""), &output, &output))
	require.NoDirExists(t, profiles)
}

func TestPruneAllRequiresConfirmationAndPreservesSource(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	history := filepath.Join(root, "execution")
	require.NoError(t, os.Mkdir(history, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(history, "report.html"), []byte("evidence"), 0600))
	source := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(source, []byte("source"), 0600))
	registry := localresources.Registry{Root: root}
	require.NoError(t, registry.RecordPath("retained", history))
	var output bytes.Buffer
	invoke := func(args []string, input string) error {
		return runCommand(context.Background(), args, root, strings.NewReader(input), &output, &output)
	}
	require.NoError(t, invoke([]string{"prune", "--all", "--dry-run"}, ""))
	require.DirExists(t, history)
	require.ErrorContains(t, invoke([]string{"prune", "--all"}, "no\n"), "cancelled")
	require.DirExists(t, history)
	require.ErrorContains(t, invoke([]string{"prune", "--all"}, ""), "cancelled")
	require.DirExists(t, history)
	require.NoError(t, invoke([]string{"prune", "--all"}, "yes\n"))
	require.NoDirExists(t, history)
	require.FileExists(t, source)
	require.NoError(t, invoke([]string{"prune", "--all", "--yes"}, ""))
}

func TestPruneConfirmationHonorsCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, confirmPrune(ctx, reader), context.Canceled)
}

func TestPruneRejectsLiveUnjournaledService(t *testing.T) {
	root := fakeService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	var output bytes.Buffer
	require.ErrorContains(t, runCommand(context.Background(), []string{"prune", "--all", "--yes"}, root, strings.NewReader(""), &output, &output), "service is still listening")
}

func TestPruneAllYesDeletesWithoutInput(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	history := filepath.Join(root, "execution")
	require.NoError(t, os.Mkdir(history, 0700))
	require.NoError(t, (localresources.Registry{Root: root}).RecordPath("retained", history))
	var output bytes.Buffer
	require.NoError(t, runCommand(context.Background(), []string{"prune", "--all", "--yes"}, root, strings.NewReader(""), &output, &output))
	require.NoDirExists(t, history)
}

func TestRecoveryDoesNotClaimUnrecordedDaemonIsStopped(t *testing.T) {
	root := fakeService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	require.ErrorContains(t, stopAllResources(context.Background(), root, control.NewClient(sessions.SocketPath(root))), "still listening")
}
