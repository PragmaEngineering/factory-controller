package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
