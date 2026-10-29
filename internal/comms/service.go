package comms

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controls/execution"
	"controls/internal/state"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Collector is a runner capability, not an MCP or workflow-engine dependency.
type Collector interface {
	Collect(context.Context, execution.Work, []string) ([]execution.Artifact, error)
}
type Service struct {
	Capacity      int
	DB            *state.Store
	Collector     Collector
	URL           string
	secret        []byte
	notifications notifier
}

func New(ctx context.Context, db *state.Store, collector Collector) (*Service, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(hex.EncodeToString(seed))
	_, err := db.Pool.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ('comms','secret',$1) ON CONFLICT DO NOTHING`, encoded)
	if err != nil {
		return nil, err
	}
	var value string
	if err = db.Get(ctx, "comms", "secret", &value); err != nil {
		return nil, err
	}
	secret, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	service := &Service{DB: db, Collector: collector, secret: secret, notifications: notifier{changed: make(chan struct{})}}
	go service.listen(ctx)
	return service, nil
}
func identity(value string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("factory-comms/"+value)).String()
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Service) token(id string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(id))
	return hex.EncodeToString(h.Sum(nil))
}
func workerID(work execution.Work) string {
	return identity(fmt.Sprintf("%s/%s/%d/%d", work.ExecutionID, work.Step.ID, work.Visit, work.Try))
}
func lockExecution(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id)
	return err
}

// Provision persists identities before launching processes. Replays recover a
// prior result/session rather than minting another worker for the same attempt.
func (s *Service) Provision(ctx context.Context, work execution.Work) (Connection, error) {
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(ctx)
	if s.Capacity > 0 {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('factory_worker_admission',0))`); err != nil {
			return Connection{}, err
		}
	}
	if err = lockExecution(ctx, tx, work.ExecutionID); err != nil {
		return Connection{}, err
	}
	// A recorded result is recoverable even when all runtime slots are busy.
	// Recovery does not reserve capacity or relaunch the completed invocation.
	var accepted []byte
	var recordedSession string
	lookup := tx.QueryRow(ctx, `SELECT result,session_id FROM factory.comms_participants WHERE id=$1`, workerID(work)).Scan(&accepted, &recordedSession)
	if lookup != nil && !errors.Is(lookup, pgx.ErrNoRows) {
		return Connection{}, lookup
	}
	if len(accepted) > 0 {
		var result execution.StepResult
		if err = json.Unmarshal(accepted, &result); err != nil {
			return Connection{}, err
		}
		connection := Connection{URL: s.URL, Token: s.token(workerID(work)), ParticipantID: workerID(work), ThreadID: threadID(work), SessionID: recordedSession, Result: &result}
		return connection, tx.Commit(ctx)
	}
	// Replacement attempts fence earlier credentials even if the previous
	// supervisor failed before it could record process shutdown.
	_, err = tx.Exec(ctx, `UPDATE factory.comms_participants SET active=false WHERE execution_id=$1 AND work->'step'->>'id'=$2 AND (work->>'visit')::bigint=$3 AND (work->>'try')::bigint<$4`, work.ExecutionID, string(work.Step.ID), work.Visit, work.Try)
	if err != nil {
		return Connection{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE factory.comms_requests SET state='cancelled' WHERE state='pending' AND owner IN (SELECT id FROM factory.comms_participants WHERE execution_id=$1 AND NOT active)`, work.ExecutionID)
	if err != nil {
		return Connection{}, err
	}
	if s.Capacity > 0 {
		ids := []string{workerID(work)}
		seen := map[string]bool{ids[0]: true}
		for _, peer := range work.Peers {
			candidate := workerID(execution.Work{ExecutionID: work.ExecutionID, Step: execution.Step{ID: peer.Step}, Visit: peer.Visit, Try: peer.Try})
			if !seen[candidate] {
				ids = append(ids, candidate)
				seen[candidate] = true
			}
		}
		var occupied int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM factory.comms_participants WHERE kind='agent' AND active AND id<>ALL($1::text[])`, ids).Scan(&occupied); err != nil {
			return Connection{}, err
		}
		if occupied+len(ids) > s.Capacity {
			return Connection{}, fault("capacity_unavailable", "round needs %d worker slots; %d of %d are occupied. Retry the round when capacity is available", len(ids), occupied, s.Capacity)
		}
	}
	id := workerID(work)
	thread := threadID(work)
	human := identity(work.ExecutionID + "/human")
	controller := identity(work.ExecutionID + "/controller")
	for _, p := range []Participant{{ID: human, Name: "human", Kind: "human"}, {ID: controller, Name: "controller", Kind: "controller"}} {
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_participants(id,execution_id,name,kind) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, p.ID, work.ExecutionID, p.Name, p.Kind)
		if err != nil {
			return Connection{}, err
		}
	}
	contextParts, _ := json.Marshal(Structured("message", map[string]any{"brief": work.Brief}).Parts)
	_, err = tx.Exec(ctx, `INSERT INTO factory.comms_threads(id,execution_id,title,context,owner_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, thread, work.ExecutionID, work.Step.Stage+" / "+string(work.Step.ID), contextParts, controller)
	if err != nil {
		return Connection{}, err
	}
	ids := []string{human, controller}
	for _, peer := range work.Peers {
		peerWork := execution.Work{ExecutionID: work.ExecutionID, Step: execution.Step{ID: peer.Step}, Visit: peer.Visit, Try: peer.Try}
		pid := workerID(peerWork)
		ids = append(ids, pid)
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_participants(id,execution_id,name,kind) VALUES($1,$2,$3,'agent') ON CONFLICT DO NOTHING`, pid, work.ExecutionID, peer.Name)
		if err != nil {
			return Connection{}, err
		}
	}
	ids = append(ids, id)
	raw, _ := json.Marshal(work)
	// Only an existing placeholder may be initialized. Never overwrite an active invocation.
	tag, err := tx.Exec(ctx, `INSERT INTO factory.comms_participants(id,execution_id,name,kind,token_hash,work) VALUES($1,$2,$3,'agent',$4,$5)
 ON CONFLICT(id) DO UPDATE SET work=EXCLUDED.work,token_hash=EXCLUDED.token_hash WHERE factory.comms_participants.work IS NULL`, id, work.ExecutionID, string(work.Step.ID), tokenHash(s.token(id)), raw)
	if err != nil {
		return Connection{}, err
	}
	for _, pid := range ids {
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_members(thread_id,participant_id,joined_sequence) VALUES($1,$2,0) ON CONFLICT DO NOTHING`, thread, pid)
		if err != nil {
			return Connection{}, err
		}
	}
	conn := Connection{URL: s.URL, Token: s.token(id), ParticipantID: id, ThreadID: thread, New: tag.RowsAffected() > 0}
	var result []byte
	if err = tx.QueryRow(ctx, `SELECT session_id,result FROM factory.comms_participants WHERE id=$1`, id).Scan(&conn.SessionID, &result); err != nil {
		return conn, err
	}
	if len(result) > 0 {
		conn.Result = new(execution.StepResult)
		if err = json.Unmarshal(result, conn.Result); err != nil {
			return conn, err
		}
	}
	return conn, tx.Commit(ctx)
}
func (s *Service) BindSession(ctx context.Context, id, session string) error {
	tag, err := s.DB.Pool.Exec(ctx, `UPDATE factory.comms_participants SET session_id=$2 WHERE id=$1 AND active AND session_id=''`, id, session)
	if err == nil && tag.RowsAffected() != 1 {
		return fault("attempt_inactive", "worker already has a session")
	}
	return err
}
func (s *Service) Authenticate(ctx context.Context, token string) (Participant, error) {
	var p Participant
	err := s.DB.Pool.QueryRow(ctx, `SELECT id,execution_id,name,kind,active FROM factory.comms_participants WHERE token_hash=$1`, tokenHash(token)).Scan(&p.ID, &p.ExecutionID, &p.Name, &p.Kind, &p.Active)
	if err != nil {
		return p, fault("not_authorized", "invalid connection credential")
	}
	return p, nil
}
func (s *Service) Result(ctx context.Context, id string) (*execution.StepResult, error) {
	var raw []byte
	err := s.DB.Pool.QueryRow(ctx, `SELECT result FROM factory.comms_participants WHERE id=$1`, id).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var result execution.StepResult
	err = json.Unmarshal(raw, &result)
	return &result, err
}
func (s *Service) End(ctx context.Context, id string) error {
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
	if _, err = tx.Exec(ctx, `UPDATE factory.comms_participants SET active=false WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE factory.comms_requests SET state='cancelled' WHERE owner=$1 AND state='pending'`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) active(ctx context.Context, tx pgx.Tx, p Participant) error {
	var active, completed bool
	err := tx.QueryRow(ctx, `SELECT active,result IS NOT NULL FROM factory.comms_participants WHERE id=$1`, p.ID).Scan(&active, &completed)
	if completed && p.Kind == "agent" {
		return fault("already_completed", "attempt already submitted its result")
	}
	if err != nil || !active {
		return fault("attempt_inactive", "participant is inactive")
	}
	// Control-plane cancellation fences writes even before process termination.
	var state string
	err = tx.QueryRow(ctx, `SELECT content->>'state' FROM factory.records WHERE kind='execution' AND id=$1`, p.ExecutionID).Scan(&state)
	if err == nil && (state == string(execution.ExecutionCancelled) || state == string(execution.ExecutionCompleted) || state == string(execution.ExecutionFailed)) {
		return fault("attempt_inactive", "execution is %s", state)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}
func (s *Service) mutate(ctx context.Context, p Participant, operation string, args any, apply func(pgx.Tx) (Receipt, error)) (Receipt, error) {
	if operation == "" || len(operation) > 128 {
		return Receipt{}, fault("invalid_argument", "operation_id must contain 1..128 characters")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return Receipt{}, err
	}
	if len(raw) > 65536 {
		return Receipt{}, fault("invalid_argument", "message exceeds 64 KiB")
	}
	fingerprint := tokenHash(string(raw))
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockExecution(ctx, tx, p.ExecutionID); err != nil {
		return Receipt{}, err
	}
	var previous string
	var receipt []byte
	err = tx.QueryRow(ctx, `SELECT fingerprint,receipt FROM factory.comms_receipts WHERE participant_id=$1 AND operation_id=$2`, p.ID, operation).Scan(&previous, &receipt)
	if err == nil {
		if previous != fingerprint {
			return Receipt{}, fault("operation_conflict", "operation_id already has different content")
		}
		var value Receipt
		err = json.Unmarshal(receipt, &value)
		return value, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, err
	}
	if err = s.active(ctx, tx, p); err != nil {
		return Receipt{}, err
	}
	var recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM factory.comms_receipts WHERE participant_id=$1 AND created_at>now()-interval '1 minute'`, p.ID).Scan(&recent); err != nil {
		return Receipt{}, err
	}
	if recent >= 120 {
		return Receipt{}, fault("temporarily_unavailable", "participant rate limit reached; retry with backoff")
	}
	value, err := apply(tx)
	if err != nil {
		return Receipt{}, err
	}
	value.OperationID = operation
	receipt, _ = json.Marshal(value)
	_, err = tx.Exec(ctx, `INSERT INTO factory.comms_receipts(participant_id,operation_id,fingerprint,receipt) VALUES($1,$2,$3,$4)`, p.ID, operation, fingerprint, receipt)
	if err != nil {
		return value, err
	}
	if err = state.AppendActivityTx(ctx, tx, state.Activity{ExecutionID: p.ExecutionID, Source: "comms/" + p.Name, Data: append(receipt, '\n'), At: time.Now().UTC()}); err != nil {
		return value, err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify('factory_comms',$1)`, p.ExecutionID)
	if err != nil {
		return value, err
	}
	return value, tx.Commit(ctx)
}
func member(ctx context.Context, tx pgx.Tx, p Participant, thread string, write bool) (Thread, error) {
	var t Thread
	var contextParts []byte
	var removed bool
	err := tx.QueryRow(ctx, `SELECT t.id,t.execution_id,t.title,t.context,t.closed,t.publication_id,t.owner_id,m.removed FROM factory.comms_threads t JOIN factory.comms_members m ON m.thread_id=t.id WHERE t.id=$1 AND m.participant_id=$2 AND t.execution_id=$3`, thread, p.ID, p.ExecutionID).Scan(&t.ID, &t.ExecutionID, &t.Title, &contextParts, &t.Closed, &t.PublicationID, &t.OwnerID, &removed)
	if err != nil || removed {
		return t, fault("not_authorized", "thread membership required")
	}
	if err = json.Unmarshal(contextParts, &t.Context); err != nil {
		return t, err
	}
	if write && t.Closed {
		return t, fault("thread_closed", "thread is closed")
	}
	return t, nil
}

// FinishExecution closes conversations and fences all remaining workers. The
// execution record is authoritative; this cleanup is safe to repeat on replay.
func (s *Service) FinishExecution(ctx context.Context, executionID string) error {
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockExecution(ctx, tx, executionID); err != nil {
		return err
	}
	for _, query := range []string{
		`UPDATE factory.comms_participants SET active=false WHERE execution_id=$1 AND kind='agent'`,
		`UPDATE factory.comms_requests SET state='cancelled' WHERE execution_id=$1 AND state='pending'`,
		`UPDATE factory.comms_threads SET closed=true WHERE execution_id=$1`,
	} {
		if _, err = tx.Exec(ctx, query, executionID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `SELECT pg_notify('factory_comms',$1)`, executionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func threadID(work execution.Work) string {
	group := string(work.Step.ID)
	if work.Step.Stage != "" {
		group = work.Step.Stage
	}
	return identity(fmt.Sprintf("%s/%s/%d", work.ExecutionID, group, work.Visit))
}
