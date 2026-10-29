package state_test

import (
	"context"
	"testing"

	"controls/internal/state"
	"controls/internal/testdb"

	"github.com/stretchr/testify/require"
)

func TestActivityStreamPreservesOutputAndCursor(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	require.NoError(t, db.AppendActivity(ctx, state.Activity{ExecutionID: "run-1", Source: "workflow", Data: []byte("started\n")}))
	require.NoError(t, db.AppendOutput(ctx, "session-1", []byte("tool event\n"), state.Activity{ExecutionID: "run-1", SessionID: "session-1", Source: "agent/coder"}))
	require.NoError(t, db.AppendActivity(ctx, state.Activity{ExecutionID: "run-2", Source: "workflow", Data: []byte("other\n")}))

	items, err := db.Activity(ctx, "run-1", 0, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "workflow", items[0].Source)
	items, err = db.Activity(ctx, "run-1", items[0].ID, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, []byte("tool event\n"), items[0].Data)
	require.Equal(t, "session-1", items[0].SessionID)
	output, err := db.Output(ctx, "session-1")
	require.NoError(t, err)
	require.Equal(t, []byte("tool event\n"), output)
}
