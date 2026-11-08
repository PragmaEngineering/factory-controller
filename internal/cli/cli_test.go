package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"controls/execution"
	"controls/internal/control"
	"controls/internal/sessions"
	"controls/internal/state"
	"controls/internal/testdb"
)

type fakeEngine struct{}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func (fakeEngine) Start(_ context.Context, request execution.StartRequest) (execution.Started, error) {
	return execution.Started{ExecutionID: request.ExecutionID, RunID: "test-run"}, nil
}
func (fakeEngine) SendDecision(context.Context, execution.Decision) error { return nil }
func (fakeEngine) SendRetry(context.Context, execution.Retry) error       { return nil }
func (fakeEngine) Cancel(context.Context, string) error                   { return nil }

func TestCLIExecContextAndInspection(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(project, "agent")
	if err := os.Mkdir(packageRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "run.sh"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := `name: fixture
agents:
  - name: builder
    path: agent
    entrypoint: run.sh
workflow:
  start: build
  steps:
    - name: build
      type: agent
      agent: builder
      on:
        completed: $done
`
	configPath := filepath.Join(project, "factory.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(root, "runtime")
	db := testdb.Open(t)
	store, err := control.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.Open(filepath.Join(runtime, "sessions"), "test-socket", db)
	if err != nil {
		t.Fatal(err)
	}
	controller := &control.Controller{Store: store, Sessions: manager, Engine: fakeEngine{}, Root: runtime}
	client := control.NewClientWithHTTPClient(&http.Client{Transport: handlerTransport{handler: control.Handler(controller)}})
	var sessionClient *sessions.Client
	call := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := Run(context.Background(), args, client, sessionClient, strings.NewReader(""), &out); err != nil {
			t.Fatalf("factory %v: %v", args, err)
		}
		return out.String()
	}
	first := call("exec", "first brief", "-c", configPath)
	if !strings.Contains(first, "STATE running") || !strings.Contains(first, "CONTEXT ") {
		t.Fatalf("bad exec response: %s", first)
	}
	var contextID, executionID string
	for _, line := range strings.Split(first, "\n") {
		if strings.HasPrefix(line, "CONTEXT ") {
			contextID = strings.TrimPrefix(line, "CONTEXT ")
		}
		if strings.HasPrefix(line, "EXECUTION ") {
			executionID = strings.TrimPrefix(line, "EXECUTION ")
		}
	}
	call("set-context", contextID)
	second := call("exec", "second brief", "-c", configPath)
	if !strings.Contains(second, "CONTEXT "+contextID) {
		t.Fatalf("selected context not reused: %s", second)
	}
	list := call("ls")
	if !strings.Contains(list, "first brief") || !strings.Contains(list, executionID) {
		t.Fatalf("execution list missing context or execution: %s", list)
	}
	if !strings.Contains(call(executionID, "-o", "json"), `"type": "execution"`) {
		t.Fatal("ID-based JSON inspection failed")
	}
	if !strings.Contains(call(executionID, "events"), "execution_created") {
		t.Fatal("execution events unavailable")
	}
	if !strings.Contains(call(executionID, "stream"), "workflow: execution_created: Execution created") {
		t.Fatal("execution stream unavailable")
	}
	if err := db.AppendOutput(context.Background(), "session-fixture", []byte("partial "), state.Activity{ExecutionID: executionID, SessionID: "session-fixture", Source: "agent/fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendOutput(context.Background(), "session-fixture", []byte("line\n"), state.Activity{ExecutionID: executionID, SessionID: "session-fixture", Source: "agent/fixture"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(call(executionID, "stream"), "agent/fixture: partial line") {
		t.Fatal("split agent line was not reassembled")
	}
	if !strings.Contains(call(executionID), "pending") {
		t.Fatal("workflow definition not visible")
	}
}
