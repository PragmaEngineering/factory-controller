package runtimehost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"controls/internal/comms"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNetworkHostRequiresCredentialAndRejectsBrowserOrigins(t *testing.T) {
	root := t.TempDir()
	token := strings.Repeat("a", 64)
	file := filepath.Join(root, "token")
	require.NoError(t, os.WriteFile(file, []byte(token), 0600))
	t.Setenv("FACTORY_RUNTIME_LISTEN", "127.0.0.1:0")
	t.Setenv("FACTORY_RUNTIME_TOKEN_FILE", file)
	t.Setenv("FACTORY_RUNTIME_PUBLIC_URL", "")
	communication := &comms.Service{}
	stop, err := serveNetwork(context.Background(), root, communication, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	require.NoError(t, err)
	defer stop()
	raw, err := os.ReadFile(filepath.Join(root, "endpoint.json"))
	require.NoError(t, err)
	var endpoint struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(raw, &endpoint))
	for _, test := range []struct {
		token, origin, host string
		status              int
	}{
		{"", "", "", 401}, {"wrong", "", "", 401}, {token, "", "", 204},
		{token, "https://browser.example", "", 403}, {token, "", "attacker.example", 403},
	} {
		request, err := http.NewRequest("GET", endpoint.URL+"/health", nil)
		require.NoError(t, err)
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		if test.origin != "" {
			request.Header.Set("Origin", test.origin)
		}
		if test.host != "" {
			request.Host = test.host
		}
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, test.status, response.StatusCode)
	}
	require.Equal(t, endpoint.URL+"/mcp", communication.URL)
}

func TestWorkspaceWritesOnlyInvocationFilesAndPreservesExistingContent(t *testing.T) {
	root := t.TempDir()
	id := uuid.NewString()
	handler := workspaceHandler(root)
	post := func(body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/workspaces/"+id, strings.NewReader(body)))
		return response
	}
	require.Equal(t, 200, post(`{"files":{"input.json":"original"}}`).Code)
	require.Equal(t, 409, post(`{"files":{"input.json":"replacement"}}`).Code)
	require.Equal(t, 400, post(`{"files":{"../escape":"bad"}}`).Code)
	data, err := os.ReadFile(filepath.Join(root, "workspaces", id, "input.json"))
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	info, err := os.Stat(filepath.Join(root, "workspaces", id, "input.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
