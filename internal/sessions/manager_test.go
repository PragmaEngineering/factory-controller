package sessions

import (
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
