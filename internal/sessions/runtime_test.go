package sessions

import (
	"controls/execution"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRetryRequiresTheSameNativeIdentity(t *testing.T) {
	original := execution.RuntimeIdentity{Name: "pi", Version: "1.0.3", AdapterVersion: "2"}
	request := Launch{StepID: "visit", Try: 2, Runtime: &original}
	previous := []Session{{StepID: "visit", Try: 1, Runtime: &original}}
	require.NoError(t, validateRetryRuntime(previous, request))
	changed := original
	for _, version := range []string{"1.0.4", "1.1.0"} {
		changed.Version = version
		previous[0].Runtime = &changed
		require.ErrorContains(t, validateRetryRuntime(previous, request), "runtime identity changed")
	}
	changed = original
	changed.AdapterVersion = "1"
	require.Error(t, validateRetryRuntime(previous, request))
	previous[0].StepID = "another-visit"
	require.NoError(t, validateRetryRuntime(previous, request))
}
