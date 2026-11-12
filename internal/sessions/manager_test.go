package sessions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controls/internal/testdb"

	"github.com/stretchr/testify/require"
)

func TestSessionLifecycleAndOutput(t *testing.T) {
	root := t.TempDir()
	db := testdb.Open(t)
	manager, err := Open(root, "", db)
	require.NoError(t, err)
	session, err := manager.Launch(Launch{
		Name: "example", Runner: "shell", Directory: root,
		Command: []string{"/bin/sh", "-c", "printf 'hello'; sleep 0.1; printf ' world'"},
	})
	require.NoError(t, err)
	waitForState(t, manager, session.ID, Exited)
	output, err := manager.Output(session.ID, 0, 100)
	require.NoError(t, err)
	require.Contains(t, output.Text, "hello world")
	require.Equal(t, int64(len(output.Text)), output.NextOffset)

	reopened, err := Open(root, "", db)
	require.NoError(t, err)
	loaded, err := reopened.Get(session.ID)
	require.NoError(t, err)
	require.Equal(t, Exited, loaded.State)
	require.NotNil(t, loaded.ExitCode)
	require.Zero(t, *loaded.ExitCode)
}

func TestPackageEnvironmentExcludesWorkflowEngineCredentials(t *testing.T) {
	environment := packageEnvironment([]string{
		"HATCHET_CLIENT_TOKEN=secret",
		"HATCHET_CLIENT_HOST_PORT=localhost:1234",
		"FACTORY_HATCHET_MODE=embedded",
		"CONTROLS_TOKEN=host-secret",
		"FABRIC_TOKEN=operator-secret",
		"FABRIC_API_TOKEN_FILE=/private/token",
		"FACTORY_RUNTIME_TOKEN_FILE=/private/host-token",
		"OPENROUTER_API_KEY=agent-key",
		"PATH=/usr/bin",
	})
	require.Equal(t, []string{"OPENROUTER_API_KEY=agent-key", "PATH=/usr/bin"}, environment)
}

func TestLaunchRejectsWorkflowEngineCredentials(t *testing.T) {
	root := t.TempDir()
	db := testdb.Open(t)
	manager, err := Open(root, "", db)
	require.NoError(t, err)
	_, err = manager.Launch(Launch{
		Name: "example", Runner: "shell", Directory: root,
		Command:     []string{"/bin/sh", "-c", "true"},
		Environment: map[string]string{"HATCHET_CLIENT_TOKEN": "secret"},
	})
	require.ErrorContains(t, err, "invalid environment name")
}

func TestQueuedMessageRequiresRunnerAcknowledgement(t *testing.T) {
	root := t.TempDir()
	db := testdb.Open(t)
	manager, err := Open(root, "", db)
	require.NoError(t, err)
	session, err := manager.Launch(Launch{
		Name: "example", Runner: "shell", Directory: root,
		Command: []string{"/bin/sh", "-c", "printf %s \"$FACTORY_SESSION_TOKEN\" > token; sleep 30"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		current, err := manager.Get(session.ID)
		if err == nil && current.State == Running {
			_ = manager.Stop(session.ID)
			waitForState(t, manager, session.ID, Stopped)
		}
	})
	message, err := manager.Send(session.ID, "please continue")
	require.NoError(t, err)
	require.Equal(t, MessageQueued, message.State)
	var token string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(filepath.Join(root, "token"))
		if err == nil && len(content) > 0 {
			token = string(content)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotEmpty(t, token)
	pending, err := manager.Inbox(session.ID, token)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, MessageDelivered, pending[0].State)
	pending, err = manager.Inbox(session.ID, token)
	require.NoError(t, err)
	require.Len(t, pending, 1, "unacknowledged delivery must be replayed")
	read, err := manager.Acknowledge(session.ID, token, message.ID)
	require.NoError(t, err)
	require.Equal(t, MessageRead, read.State)
	pending, err = manager.Inbox(session.ID, token)
	require.NoError(t, err)
	require.Empty(t, pending)
	other, err := Open(filepath.Join(root, "other-worker"), "", db)
	require.NoError(t, err)
	_, err = other.Send(session.ID, "from another manager")
	require.NoError(t, err)
	pending, err = manager.Inbox(session.ID, token)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "from another manager", pending[0].Body)
}

func TestPTYMessageDelivery(t *testing.T) {
	root := t.TempDir()
	db := testdb.Open(t)
	manager, err := Open(root, "", db)
	require.NoError(t, err)
	session, err := manager.Launch(Launch{
		Name: "example", Runner: "shell", InputMode: "pty", Directory: root,
		Command: []string{"/bin/sh", "-c", "read line; printf 'received:%s\\n' \"$line\""},
	})
	require.NoError(t, err)
	_, err = manager.Send(session.ID, "hello")
	require.NoError(t, err)
	waitForState(t, manager, session.ID, Exited)
	output, err := manager.Output(session.ID, 0, 100)
	require.NoError(t, err)
	require.True(t, strings.Contains(output.Text, "received:hello"), output.Text)
	messages, err := manager.Messages(session.ID)
	require.NoError(t, err)
	require.Equal(t, MessageDelivered, messages[0].State)
}

func waitForState(t *testing.T, manager *Manager, id string, expected State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		session, err := manager.Get(id)
		require.NoError(t, err)
		if session.State == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s did not reach %s", id, expected)
}

func TestResourceProjectionAndBudgetNoticesSurviveReopen(t *testing.T) {
	root := t.TempDir()
	db := testdb.Open(t)
	manager, err := Open(root, "", db)
	require.NoError(t, err)
	line := `{"type":"factory.telemetry.v1","sequence":1,"kind":"usage","tokens":{"input":10,"cache_read":90,"output":3}}`
	session, err := manager.Launch(Launch{Name: "fixture", Runner: "shell", ExecutionID: "execution", Directory: root, Command: []string{"/bin/sh", "-c", "printf '%s\\n' '" + line + "'"}})
	require.NoError(t, err)
	waitForState(t, manager, session.ID, Exited)
	require.NoError(t, manager.ReportBudget(session.ID, "tokens/warning", "Budget warning"))
	require.NoError(t, manager.ReportBudget(session.ID, "tokens/warning", "Budget warning"))
	manager, err = Open(root, "", db)
	require.NoError(t, err)
	loaded, err := manager.Get(session.ID)
	require.NoError(t, err)
	require.EqualValues(t, 103, loaded.Resources.Tokens.Total())
	require.Len(t, loaded.BudgetNotices, 1)
	require.Equal(t, "unknown", loaded.Resources.Cost())
	items, err := db.Activity(context.Background(), "execution", 0, 500)
	require.NoError(t, err)
	count := 0
	for _, item := range items {
		if item.Source == "budget/fixture" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestShutdownStopsOwnedProcessesAndRejectsNewLaunches(t *testing.T) {
	db := testdb.Open(t)
	root := t.TempDir()
	manager, err := Open(root, "", db)
	require.NoError(t, err)
	launch := Launch{Name: "worker", Runner: "command", Directory: root, Command: []string{"/bin/sh", "-c", "printf started; sleep 30"}}
	session, err := manager.Launch(launch)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, manager.Shutdown(ctx))
	current, err := manager.Get(session.ID)
	require.NoError(t, err)
	require.Equal(t, Stopped, current.State)
	_, err = manager.Launch(launch)
	require.ErrorContains(t, err, "shutting down")
	require.NoError(t, manager.Shutdown(ctx))
}
