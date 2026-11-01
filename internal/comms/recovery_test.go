package comms_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"controls/execution"
	"controls/internal/comms"
	"controls/internal/testdb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCompletionRecoveryIsDurableBoundedAndScoped(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	service, err := comms.New(ctx, db, nil)
	require.NoError(t, err)
	service.Recovery = comms.RecoveryPolicy{IdleAfter: time.Minute, Interval: time.Minute, Limit: 2}
	fixture := func(peers bool) (execution.Work, comms.Connection, comms.Participant, string) {
		work := execution.Work{ExecutionID: uuid.NewString(), Step: execution.Step{ID: "build", Stage: "implementation", Routes: map[execution.Outcome]execution.StepID{"ready": execution.Complete}}, Package: execution.PackageSnapshot{Name: "worker"}, Visit: 1, Try: 1}
		if peers {
			work.Peers = []execution.Peer{{Step: "build", Name: "build", Visit: 1, Try: 1}, {Step: "review", Name: "review", Visit: 1, Try: 1}}
		}
		c, err := service.Provision(ctx, work)
		require.NoError(t, err)
		p, err := service.Authenticate(ctx, c.Token)
		require.NoError(t, err)
		members, err := service.Participants(ctx, p, c.ThreadID)
		require.NoError(t, err)
		controller := ""
		for _, m := range members {
			if m.Kind == "controller" {
				controller = m.ID
			}
		}
		require.NotEmpty(t, controller)
		return work, c, p, controller
	}
	status := func(c comms.Connection, p comms.Participant, controller, state string) {
		_, err := service.Send(ctx, p, comms.Send{OperationID: uuid.NewString(), ThreadID: c.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("runtime_status", map[string]any{"state": state})})
		require.NoError(t, err)
	}
	age := func(id string) {
		_, err := db.Pool.Exec(ctx, `UPDATE factory.comms_completion SET state_since=now()-interval '3 minutes',last_reminded_at=now()-interval '3 minutes' WHERE participant_id=$1`, id)
		require.NoError(t, err)
	}
	t.Run("rejection sends one scoped correction and accepted correction stops recovery", func(t *testing.T) {
		work, c, p, controller := fixture(false)
		_, err := service.Send(ctx, p, comms.Send{OperationID: "bad", ThreadID: c.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "Done", "artifacts": []string{"report.md"}})})
		require.ErrorContains(t, err, "array of objects")
		// Two supervisors/replay calls must not enqueue two copies of a reminder.
		var group sync.WaitGroup
		failures := make(chan error, 2)
		for range 2 {
			group.Add(1)
			go func() { defer group.Done(); failures <- service.Recover(ctx, p.ID) }()
		}
		group.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		batch, err := service.Read(ctx, p, comms.Read{ThreadID: c.ThreadID})
		require.NoError(t, err)
		require.Len(t, batch.Messages, 1)
		require.Equal(t, controller, batch.Messages[0].Sender)
		require.Equal(t, []string{p.ID}, batch.Messages[0].Audience)
		body, _ := json.Marshal(batch.Messages[0].Payload)
		require.Contains(t, string(body), "artifacts must be an array of objects")
		require.Contains(t, string(body), "send_message")
		require.Contains(t, string(body), "Do not repeat")
		corrected := comms.Send{OperationID: "corrected", ThreadID: c.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "Completed and checked"})}
		receipt, err := service.Send(ctx, p, corrected)
		require.NoError(t, err)
		require.Equal(t, "accepted", receipt.Effect.State)
		repeat, err := service.Send(ctx, p, corrected)
		require.NoError(t, err)
		require.Equal(t, receipt.MessageID, repeat.MessageID)
		age(p.ID)
		require.NoError(t, service.Recover(ctx, p.ID))
		messages, err := service.AuditMessages(ctx, work.ExecutionID)
		require.NoError(t, err)
		require.Len(t, messages, 2)
	})
	t.Run("settled incomplete worker is reminded then fenced after a restart", func(t *testing.T) {
		_, c, p, controller := fixture(false)
		status(c, p, controller, "busy")
		age(p.ID)
		require.NoError(t, service.Recover(ctx, p.ID))
		var count int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT reminders FROM factory.comms_completion WHERE participant_id=$1`, p.ID).Scan(&count))
		require.Zero(t, count)
		status(c, p, controller, "idle")
		age(p.ID)
		require.NoError(t, service.Recover(ctx, p.ID))
		// Repeated idle reports cannot extend the deadline or reset the counter.
		status(c, p, controller, "idle")
		age(p.ID)
		reopened, err := comms.New(ctx, db, nil)
		require.NoError(t, err)
		reopened.Recovery = service.Recovery
		require.NoError(t, reopened.Recover(ctx, p.ID))
		age(p.ID)
		require.ErrorContains(t, reopened.Recover(ctx, p.ID), "no accepted step_result after 2 controller reminders")
		require.ErrorContains(t, service.Recover(ctx, p.ID), "recovery exhausted")
		_, err = service.Send(ctx, p, comms.Send{OperationID: "too-late", ThreadID: c.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "Done"})})
		require.ErrorContains(t, err, "inactive")
		result, err := service.Result(ctx, p.ID)
		require.NoError(t, err)
		require.Nil(t, result)
	})
	t.Run("idle peer and human waits are exempt but all-idle groups recover", func(t *testing.T) {
		work, c, p, controller := fixture(true)
		peerWork := work
		peerWork.Step.ID = "review"
		peerWork.Package.Name = "review"
		peerConnection, err := service.Provision(ctx, peerWork)
		require.NoError(t, err)
		peer, err := service.Authenticate(ctx, peerConnection.Token)
		require.NoError(t, err)
		status(c, p, controller, "idle")
		age(p.ID)
		require.NoError(t, service.Recover(ctx, p.ID)) // peer status unknown
		status(peerConnection, peer, controller, "busy")
		require.NoError(t, service.Recover(ctx, p.ID))
		status(peerConnection, peer, controller, "idle")
		age(peer.ID)
		members, err := service.Participants(ctx, p, c.ThreadID)
		require.NoError(t, err)
		human := ""
		for _, m := range members {
			if m.Kind == "human" {
				human = m.ID
			}
		}

		_, err = service.Send(ctx, p, comms.Send{OperationID: "invalid-before-human", ThreadID: c.ThreadID, Recipients: []string{controller}, Payload: comms.Structured("step_result", map[string]any{"outcome": "ready", "summary": "done", "artifacts": []string{"bad.md"}})})
		require.ErrorContains(t, err, "array of objects")
		_, err = service.Send(ctx, p, comms.Send{OperationID: "question", ThreadID: c.ThreadID, Recipients: []string{controller, human}, Payload: comms.Structured("input_request", map[string]any{"prompt": "Need a decision"})})
		require.NoError(t, err)
		require.NoError(t, service.Recover(ctx, p.ID))
		var count int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT reminders FROM factory.comms_completion WHERE participant_id=$1`, p.ID).Scan(&count))
		require.Zero(t, count)
		_, err = db.Pool.Exec(ctx, `UPDATE factory.comms_requests SET state='answered' WHERE owner=$1`, p.ID)
		require.NoError(t, err)
		require.NoError(t, service.Recover(ctx, p.ID))
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT reminders FROM factory.comms_completion WHERE participant_id=$1`, p.ID).Scan(&count))
		require.Equal(t, 1, count)
	})
}
