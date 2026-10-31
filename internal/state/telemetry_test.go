package state_test

import (
	"context"
	"controls/internal/state"
	"controls/internal/telemetry"
	"controls/internal/testdb"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOutputAndTelemetryCommitTogether(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	line := `{"type":"factory.telemetry.v1","sequence":1,"kind":"usage","tokens":{"input":7,"cache_read":93}}` + "\n"
	activity := state.Activity{ExecutionID: "run", SessionID: "session", Source: "agent/fixture"}
	require.NoError(t, db.AppendOutput(ctx, "session", []byte(line[:30]), activity))
	var p telemetry.Projection
	require.NoError(t, db.Get(ctx, "telemetry", "session", &p))
	require.Zero(t, p.Summary.Samples)
	require.NoError(t, db.AppendOutput(ctx, "session", []byte(line[30:]), activity))
	require.NoError(t, db.Get(ctx, "telemetry", "session", &p))
	require.EqualValues(t, 100, p.Summary.Tokens.Total())
	raw, err := db.Output(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, line, string(raw))
	require.NoError(t, db.AppendOutput(ctx, "session", []byte(line), activity))
	require.NoError(t, db.Get(ctx, "telemetry", "session", &p))
	require.EqualValues(t, 100, p.Summary.Tokens.Total())
}
