package comms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"controls/execution"
	"controls/internal/state"
	"github.com/a2aproject/a2a-go/a2a"
	"github.com/jackc/pgx/v5"
)

// RecoveryPolicy bounds protocol repair, not the worker's implementation time.
// Zero values select defaults. Persisted counters survive service replay/restart.
type RecoveryPolicy struct {
	IdleAfter time.Duration
	Interval  time.Duration
	Limit     int
}

func (p RecoveryPolicy) defaults() RecoveryPolicy {
	if p.IdleAfter <= 0 {
		p.IdleAfter = 2 * time.Minute
	}
	if p.Interval <= 0 {
		p.Interval = 30 * time.Second
	}
	if p.Limit <= 0 {
		p.Limit = 2
	}
	return p
}

// runtime_status is emitted by adapters, never inferred from quiet process
// output. It is an observation under the worker identity, not proof of work.
func (s *Service) applyRuntimeStatus(ctx context.Context, tx pgx.Tx, p Participant, m *Message) (*Effect, error) {
	if p.Kind != "agent" {
		return nil, fault("not_authorized", "runtime status requires an assigned worker")
	}
	var workRaw []byte
	if err := tx.QueryRow(ctx, `SELECT work FROM factory.comms_participants WHERE id=$1`, p.ID).Scan(&workRaw); err != nil {
		return nil, err
	}
	var work execution.Work
	if err := json.Unmarshal(workRaw, &work); err != nil {
		return nil, err
	}
	if m.ThreadID != threadID(work) {
		return nil, fault("not_authorized", "runtime status requires the assigned control thread")
	}
	data, err := decodeData[struct {
		State string `json:"state"`
	}](m.Payload)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO factory.comms_completion(participant_id,runtime_state,state_since) VALUES($1,$2,$3)
 ON CONFLICT(participant_id) DO UPDATE SET runtime_state=$2,state_since=CASE WHEN factory.comms_completion.runtime_state=$2 THEN factory.comms_completion.state_since ELSE $3 END`, p.ID, data.State, m.At)
	return &Effect{State: "accepted"}, err
}

func (s *Service) recordRejectedResult(ctx context.Context, p Participant, args Send, cause error) error {
	var failure *Fault
	if p.Kind != "agent" || !errors.As(cause, &failure) || !contains([]string{"invalid_argument", "invalid_outcome", "invalid_result", "artifact_validation_failed", "stale_revision"}, failure.Code) {
		return nil
	}
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockExecution(ctx, tx, p.ExecutionID); err != nil {
		return err
	}
	if err = s.active(ctx, tx, p); err != nil {
		return nil
	}
	var workRaw, result []byte
	if err = tx.QueryRow(ctx, `SELECT work,result FROM factory.comms_participants WHERE id=$1`, p.ID).Scan(&workRaw, &result); err != nil {
		return err
	}
	if len(result) > 0 {
		return nil
	}
	var work execution.Work
	if err = json.Unmarshal(workRaw, &work); err != nil {
		return err
	}
	if args.ThreadID != threadID(work) {
		return nil
	}
	if _, err = member(ctx, tx, p, args.ThreadID, true); err != nil {
		return nil
	}
	// Only bounded, safe protocol guidance is stored. No rejected payload values,
	// artifact contents or raw infrastructure diagnostics are copied into prompts.
	reason := failure.Code + ": " + failure.Message
	if failure.Code == "invalid_outcome" {
		reason = "invalid_outcome: choose an outcome from the assignment's allowed outcomes"
	}
	if failure.Code == "stale_revision" {
		reason = "stale_revision: reference the current publication returned by the thread; publish changed output files before claiming readiness"
	}
	if failure.Code == "artifact_validation_failed" {
		reason = "artifact_validation_failed: required output files could not be verified; inspect the tool error and repair the assigned outputs"
	}
	_, err = tx.Exec(ctx, `INSERT INTO factory.comms_completion(participant_id,rejection) VALUES($1,$2) ON CONFLICT(participant_id) DO UPDATE SET rejection=$2`, p.ID, reason)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]string{"kind": "completion_rejected", "code": failure.Code, "operation_id": args.OperationID})
	if err = state.AppendActivityTx(ctx, tx, state.Activity{ExecutionID: p.ExecutionID, Source: "comms/" + p.Name, Data: append(raw, '\n'), At: time.Now().UTC()}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Recover is called by the invocation supervisor. The execution lock serializes
// it with result acceptance, making reminders and exhaustion safe under replay
// and multiple supervisors. It never chooses an outcome or submits a result.
func (s *Service) Recover(ctx context.Context, id string) error {
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var executionID string
	if err = tx.QueryRow(ctx, `SELECT execution_id FROM factory.comms_participants WHERE id=$1`, id).Scan(&executionID); err != nil {
		return err
	}
	if err = lockExecution(ctx, tx, executionID); err != nil {
		return err
	}
	var p Participant
	var workRaw, result []byte
	if err = tx.QueryRow(ctx, `SELECT id,execution_id,name,kind,active,work,result FROM factory.comms_participants WHERE id=$1`, id).Scan(&p.ID, &p.ExecutionID, &p.Name, &p.Kind, &p.Active, &workRaw, &result); err != nil {
		return err
	}
	if len(result) > 0 {
		return nil
	}
	var runtime, rejection, blocked string
	var since, last *time.Time
	var attempts int
	err = tx.QueryRow(ctx, `SELECT runtime_state,state_since,rejection,reminders,last_reminded_at,blocked_reason FROM factory.comms_completion WHERE participant_id=$1`, id).Scan(&runtime, &since, &rejection, &attempts, &last, &blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if blocked != "" {
		return fmt.Errorf("completion recovery exhausted: %s", blocked)
	}
	if !p.Active {
		return nil
	}
	var work execution.Work
	if err = json.Unmarshal(workRaw, &work); err != nil {
		return err
	}
	policy := s.Recovery.defaults()
	now := time.Now().UTC()
	if last != nil && now.Sub(*last) < policy.Interval {
		return nil
	}
	// A formal human request prevents result acceptance until answered. Do not
	// demand an impossible completion even if an earlier result was rejected.
	var inputPending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_requests WHERE thread_id=$1 AND state='pending')`, threadID(work)).Scan(&inputPending); err != nil {
		return err
	}
	if inputPending {
		return nil
	}
	if rejection == "" {
		if runtime != "idle" || since == nil || now.Sub(*since) < policy.IdleAfter {
			return nil
		}
		// A live peer may still be doing useful work. Unknown runtime status is
		// treated conservatively. Once every incomplete peer is observed idle,
		// protocol recovery can prompt the group instead of waiting indefinitely.
		var waiting bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(
   SELECT 1 FROM factory.comms_members m JOIN factory.comms_participants p ON p.id=m.participant_id
   LEFT JOIN factory.comms_completion c ON c.participant_id=p.id
   WHERE m.thread_id=$1 AND NOT m.removed AND p.id<>$2 AND p.kind='agent' AND p.active AND p.result IS NULL
   AND (c.runtime_state IS NULL OR c.runtime_state<>'idle'))`, threadID(work), id).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
	}
	if attempts >= policy.Limit {
		blocked = fmt.Sprintf("no accepted step_result after %d controller reminders", attempts)
		if rejection != "" {
			blocked += "; last rejection: " + rejection
		}
		if _, err = tx.Exec(ctx, `UPDATE factory.comms_completion SET blocked_reason=$2 WHERE participant_id=$1`, id, blocked); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE factory.comms_participants SET active=false WHERE id=$1`, id); err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"kind": "completion_recovery_exhausted", "reason": blocked})
		if err = state.AppendActivityTx(ctx, tx, state.Activity{ExecutionID: p.ExecutionID, Source: "comms/" + p.Name, Data: append(raw, '\n'), At: now}); err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		return fmt.Errorf("completion recovery exhausted: %s", blocked)
	}
	outcomes := []string{}
	for outcome := range work.Step.Routes {
		outcomes = append(outcomes, string(outcome))
	}
	sort.Strings(outcomes)
	reason := "Your runtime reported an idle turn, but no step_result has been accepted. If your assigned work is complete, submit its result now; otherwise continue the assignment or request concrete missing input."
	if rejection != "" {
		reason = "Your step_result was rejected: " + rejection + ". Correct the handoff; do not repeat completed implementation or verification."
	}
	guidance := CompletionExample(threadID(work), outcomes)
	var packageSettings struct {
		Protocol string `json:"protocol"`
	}
	if json.Unmarshal(work.Package.Settings, &packageSettings) == nil && packageSettings.Protocol == "mcp" {
		guidance = fmt.Sprintf("Use assignment_complete with output containing outcome (one of %v), summary, and applicable data/publication_id. Do not repeat completed work. Correct rejected output against assignment_get and retry unchanged only for uncertain transport.", outcomes)
	}
	body := fmt.Sprintf("Controller completion recovery %d/%d. %s\n%s", attempts+1, policy.Limit, reason, guidance)
	controller := Participant{ID: identity(p.ExecutionID + "/controller"), Name: "controller", Kind: "controller", ExecutionID: p.ExecutionID}
	m := Message{ID: identity(fmt.Sprintf("%s/completion-reminder/%d", id, attempts+1)), ThreadID: threadID(work), Sender: controller.ID, SenderName: controller.Name, Audience: []string{id}, At: now, Payload: Payload{Kind: "message", Version: 1, Parts: a2a.ContentParts{a2a.TextPart{Text: body}}}}
	if err = tx.QueryRow(ctx, `UPDATE factory.comms_threads SET next_sequence=next_sequence+1 WHERE id=$1 AND NOT closed RETURNING next_sequence`, m.ThreadID).Scan(&m.Sequence); err != nil {
		return err
	}
	raw, _ := json.Marshal(m.Payload)
	if _, err = tx.Exec(ctx, `INSERT INTO factory.comms_messages(id,thread_id,sequence,sender,audience,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, m.ID, m.ThreadID, m.Sequence, m.Sender, m.Audience, raw, now); err != nil {
		return err
	}
	if err = appendMessageActivity(ctx, tx, controller, m); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE factory.comms_completion SET reminders=reminders+1,last_reminded_at=$2 WHERE participant_id=$1`, id, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_notify('factory_comms',$1)`, p.ExecutionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
