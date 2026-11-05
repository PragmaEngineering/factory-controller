package app

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExplicitSelfHostedModeRequiresServiceToken(t *testing.T) {
	t.Setenv("FACTORY_HATCHET_MODE", "self-hosted")
	t.Setenv("HATCHET_CLIENT_TOKEN", "")
	_, err := newHatchetClient()
	if err == nil || !strings.Contains(err.Error(), "service-issued client token") {
		t.Fatalf("expected self-hosted configuration error, got %v", err)
	}
}

func TestUnknownHatchetModeFails(t *testing.T) {
	t.Setenv("FACTORY_HATCHET_MODE", "cloud")
	_, err := newHatchetClient()
	if err == nil || !strings.Contains(err.Error(), "unsupported FACTORY_HATCHET_MODE") {
		t.Fatalf("expected unsupported mode error, got %v", err)
	}
}

func TestServiceShutdownIsExplicit(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		stopped := false
		handler := serviceLifecycleHandler(http.NotFoundHandler(), func() { stopped = true })
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/shutdown", nil))
		if method == http.MethodPost {
			require.Equal(t, http.StatusAccepted, response.Code)
			require.True(t, stopped)
		} else {
			require.Equal(t, http.StatusMethodNotAllowed, response.Code)
			require.False(t, stopped)
		}
	}
}
