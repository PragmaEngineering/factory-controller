package runtimehost

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"controls/internal/sessions"
	"controls/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestStandaloneHostWithoutHatchet(t *testing.T) {
	testdb.Start(t)
	// Any accidental workflow startup would fail this configuration.
	t.Setenv("FACTORY_HATCHET_MODE", "invalid-no-engine")
	root, err := os.MkdirTemp("/tmp", "fr-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, root) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Error("host failed to shut down")
		}
	})
	client := sessions.NewClient(sessions.SocketPath(root))
	require.Eventually(t, func() bool { return client.Health(ctx) == nil }, 10*time.Second, 20*time.Millisecond)
	session, err := client.Launch(ctx, sessions.Launch{Name: "fixture", Runner: "command", Directory: root, Command: []string{"/bin/sh", "-c", "echo standalone; sleep 30"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		out, e := client.Output(ctx, session.ID, 0)
		return e == nil && strings.Contains(out.Text, "standalone")
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, client.Stop(ctx, session.ID))
	httpClient := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sessions.SocketPath(root))
	}}}
	defer httpClient.CloseIdleConnections()
	response, err := httpClient.Post("http://local/comms/scopes", "application/json", strings.NewReader(`{"name":"host-test"}`))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var scope map[string]string
	require.NoError(t, json.NewDecoder(response.Body).Decode(&scope))
	require.True(t, strings.HasPrefix(scope["id"], "scope:"))
	response2, err := httpClient.Post("http://local/comms/scopes/"+scope["id"]+"/participants", "application/json", strings.NewReader(`{"key":"operator-1","name":"operator"}`))
	require.NoError(t, err)
	defer response2.Body.Close()
	require.Equal(t, http.StatusOK, response2.StatusCode)
	var credentials map[string]string
	require.NoError(t, json.NewDecoder(response2.Body).Decode(&credentials))
	require.NotEmpty(t, credentials["token"])
	require.NotEmpty(t, credentials["url"])
	auth, err := httpClient.Post("http://local/comms/authenticate", "application/json", strings.NewReader(`{"token":"`+credentials["token"]+`"}`))
	require.NoError(t, err)
	defer auth.Body.Close()
	require.Equal(t, http.StatusOK, auth.StatusCode)
	var peer map[string]any
	require.NoError(t, json.NewDecoder(auth.Body).Decode(&peer))
	require.Equal(t, credentials["participant_id"], peer["id"])
	inspect, err := httpClient.Get("http://local/comms/participants/" + credentials["participant_id"])
	require.NoError(t, err)
	defer inspect.Body.Close()
	require.Equal(t, http.StatusOK, inspect.StatusCode)
	denied, err := httpClient.Post("http://local/comms/authenticate", "application/json", strings.NewReader(`{"token":"invalid"}`))
	require.NoError(t, err)
	defer denied.Body.Close()
	require.Equal(t, http.StatusBadRequest, denied.StatusCode)

}
