package comms_test

import (
	"context"
	"encoding/json"
	"testing"

	"controls/internal/comms"
	"controls/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestStandalonePeers(t *testing.T) {
	db := testdb.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	stop, err := s.Listen(ctx)
	require.NoError(t, err)
	defer stop()
	scope, err := s.CreateScope(ctx, "fabric-test")
	require.NoError(t, err)
	repeated, err := s.CreateScope(ctx, "fabric-test")
	require.NoError(t, err)
	require.Equal(t, scope, repeated)
	a, err := s.RegisterPeer(ctx, scope.ID, "a", "author")
	require.NoError(t, err)
	b, err := s.RegisterPeer(ctx, scope.ID, "b", "reviewer")
	require.NoError(t, err)
	again, err := s.RegisterPeer(ctx, scope.ID, "a", "author")
	require.NoError(t, err)
	require.Equal(t, a.Token, again.Token)
	_, err = s.RegisterPeer(ctx, scope.ID, "a", "changed")
	require.ErrorContains(t, err, "operation_conflict")
	pa, err := s.Authenticate(ctx, a.Token)
	require.NoError(t, err)
	pb, err := s.Authenticate(ctx, b.Token)
	require.NoError(t, err)
	clientA := connect(t, ctx, a)
	clientB := connect(t, ctx, b)
	var thread comms.Receipt
	require.NoError(t, json.Unmarshal(call(t, ctx, clientA, "create_thread", comms.Create{OperationID: "thread", Title: "Review", Participants: []string{b.ParticipantID}}), &thread))
	args := comms.Send{OperationID: "send", ThreadID: thread.ThreadID, Recipients: []string{b.ParticipantID}, Payload: comms.Structured("fabric.result", map[string]any{"value": 42})}
	first := call(t, ctx, clientA, "send_message", args)
	require.JSONEq(t, string(first), string(call(t, ctx, clientA, "send_message", args)))
	var batch comms.Batch
	require.NoError(t, json.Unmarshal(call(t, ctx, clientB, "wait_for_message", comms.Read{ThreadID: thread.ThreadID, WaitSeconds: 1}), &batch))
	require.Len(t, batch.Messages, 1)
	// A new service/client recovers identities, credentials, cursors and receipts.
	restored, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	recovered, err := restored.RegisterPeer(ctx, scope.ID, "b", "reviewer")
	require.NoError(t, err)
	require.Equal(t, b.Token, recovered.Token)
	read, err := restored.Read(ctx, pb, comms.Read{ThreadID: thread.ThreadID, AckThrough: batch.Cursor, Mode: "unread"})
	require.NoError(t, err)
	require.Empty(t, read.Messages)
	_, err = s.Send(ctx, pa, comms.Send{OperationID: "workflow", ThreadID: thread.ThreadID, Payload: comms.Structured("step_result", map[string]any{"outcome": "done", "summary": "no workflow"})})
	require.Error(t, err)
	other, err := s.CreateScope(ctx, "other")
	require.NoError(t, err)
	c, err := s.RegisterPeer(ctx, other.ID, "c", "outsider")
	require.NoError(t, err)
	pc, err := s.Authenticate(ctx, c.Token)
	require.NoError(t, err)
	_, err = s.Read(ctx, pc, comms.Read{ThreadID: thread.ThreadID})
	require.ErrorContains(t, err, "not_authorized")
	require.NoError(t, s.RetirePeer(ctx, b.ParticipantID))
	_, err = s.Read(ctx, pb, comms.Read{ThreadID: thread.ThreadID})
	require.ErrorContains(t, err, "attempt_inactive")
	_, err = s.RegisterPeer(ctx, scope.ID, "b", "reviewer")
	require.ErrorContains(t, err, "attempt_inactive")
}
