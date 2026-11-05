package comms_test

import (
	"context"
	"controls/execution"
	"controls/internal/comms"
	"controls/internal/testdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestLifecycleReportsAreControllerOnly(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	service, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	work := execution.Work{ExecutionID: uuid.NewString(), Step: execution.Step{ID: "author", Stage: "pair", Routes: map[execution.Outcome]execution.StepID{"completed": execution.Complete}}, Package: execution.PackageSnapshot{Name: "author"}, Visit: 1, Try: 1, Peers: []execution.Peer{{Step: "author", Name: "author", Visit: 1, Try: 1}, {Step: "reviewer", Name: "reviewer", Visit: 1, Try: 1}}}
	authorConnection, err := service.Provision(ctx, work)
	require.NoError(t, err)
	author, err := service.Authenticate(ctx, authorConnection.Token)
	require.NoError(t, err)
	work.Step.ID = "reviewer"
	work.Package.Name = "reviewer"
	reviewerConnection, err := service.Provision(ctx, work)
	require.NoError(t, err)
	reviewer, err := service.Authenticate(ctx, reviewerConnection.Token)
	require.NoError(t, err)
	members, err := service.Participants(ctx, author, authorConnection.ThreadID)
	require.NoError(t, err)
	var controller string
	var human comms.Participant
	for _, member := range members {
		if member.Kind == "controller" {
			controller = member.ID
		}
		if member.Kind == "human" {
			human = member
		}
	}
	require.NotEmpty(t, controller)
	require.NotEmpty(t, human.ID)
	for _, recipients := range [][]string{nil, {}, {reviewer.ID}, {controller, reviewer.ID}, {controller, human.ID}, {controller, controller}} {
		_, err := service.Send(ctx, author, comms.Send{OperationID: uuid.NewString(), ThreadID: authorConnection.ThreadID, Recipients: recipients, Payload: comms.Structured("runtime_status", map[string]any{"state": "busy"})})
		require.ErrorContains(t, err, "runtime_status requires exactly one recipient")
	}
	var group sync.WaitGroup
	failures := make(chan error, 2)
	for _, worker := range []comms.Participant{author, reviewer} {
		group.Add(1)
		go func() {
			defer group.Done()
			for _, state := range []string{"busy", "idle"} {
				_, err := service.Send(ctx, worker, comms.Send{OperationID: uuid.NewString(), ThreadID: authorConnection.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("runtime_status", map[string]any{"state": state})})
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	for _, worker := range []comms.Participant{author, reviewer} {
		batch, err := service.Read(ctx, worker, comms.Read{ThreadID: authorConnection.ThreadID})
		require.NoError(t, err)
		require.Len(t, batch.Messages, 2) // Senders retain their own audit visibility.
		for _, message := range batch.Messages {
			require.Equal(t, worker.ID, message.Sender)
			require.Equal(t, []string{controller}, message.Audience)
		}
		var state string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT runtime_state FROM factory.comms_completion WHERE participant_id=$1`, worker.ID).Scan(&state))
		require.Equal(t, "idle", state)
	}
	inbox, err := service.Read(ctx, human, comms.Read{ThreadID: authorConnection.ThreadID})
	require.NoError(t, err)
	require.Len(t, inbox.Messages, 4) // The human may inspect audit history without being a recipient.
	for _, message := range inbox.Messages {
		require.NotContains(t, message.Audience, human.ID)
	}
	audit, err := service.AuditMessages(ctx, work.ExecutionID)
	require.NoError(t, err)
	require.Len(t, audit, 4)
	_, err = service.Send(ctx, author, comms.Send{OperationID: uuid.NewString(), ThreadID: authorConnection.ThreadID, Recipients: []string{reviewer.ID}, Payload: comms.Structured("message", map[string]any{"text": "Review the publication"})})
	require.NoError(t, err)
	batch, err := service.Read(ctx, reviewer, comms.Read{ThreadID: authorConnection.ThreadID})
	require.NoError(t, err)
	require.Len(t, batch.Messages, 3)
	require.Equal(t, "message", batch.Messages[2].Payload.Kind)
}
