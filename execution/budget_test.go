package execution

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBudgetValidation(t *testing.T) {
	require.NoError(t, (*Budget)(nil).Validate())
	for _, b := range []Budget{{Tokens: 1, Action: "unknown"}, {Tokens: 1, Action: "warn", WarnPercent: 101}, {Action: "pause"}} {
		require.Error(t, b.Validate())
	}
	require.NoError(t, (&Budget{Tokens: 100, ElapsedSeconds: 60, WarnPercent: 80, Action: "pause"}).Validate())
}
