package app

import (
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
