package comms

import (
	"context"
	"encoding/json"

	"controls/internal/state"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Create(ctx context.Context, p Participant, args Create) (Receipt, error) {
	return s.mutate(ctx, p, args.OperationID, struct {
		Tool string
		Args Create
	}{"create_thread", args}, func(tx pgx.Tx) (Receipt, error) {
		if args.Title == "" || len(args.Title) > 200 || len(args.Participants) > 32 {
			return Receipt{}, fault("invalid_argument", "title and at most 32 participants required")
		}
		id := uuid.NewString()
		raw, err := json.Marshal(args.Context)
		if err != nil {
			return Receipt{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO factory.comms_threads(id,execution_id,title,context,owner_id) VALUES($1,$2,$3,$4,$5)`, id, p.ExecutionID, args.Title, raw, p.ID)
		if err != nil {
			return Receipt{}, err
		}
		ids := append(append([]string{}, args.Participants...), p.ID)
		for _, pid := range ids {
			var exists bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_participants WHERE id=$1 AND execution_id=$2 AND active)`, pid, p.ExecutionID).Scan(&exists)
			if err != nil {
				return Receipt{}, err
			}
			if !exists {
				return Receipt{}, fault("not_authorized", "participant must belong to this execution")
			}
			// Workers may create conversations among existing peers, never new identities.
			_, err = tx.Exec(ctx, `INSERT INTO factory.comms_members(thread_id,participant_id,joined_sequence) VALUES($1,$2,0) ON CONFLICT DO NOTHING`, id, pid)
			if err != nil {
				return Receipt{}, err
			}
		}
		return Receipt{ThreadID: id, State: "created"}, nil
	})
}
func (s *Service) Membership(ctx context.Context, p Participant, tool string, args Membership) (Receipt, error) {
	return s.mutate(ctx, p, args.OperationID, struct {
		Tool string
		Args Membership
	}{tool, args}, func(tx pgx.Tx) (Receipt, error) {

		t, err := member(ctx, tx, p, args.ThreadID, false)
		if err != nil {
			return Receipt{}, err
		}
		if p.Kind != "human" && p.Kind != "controller" && t.OwnerID != p.ID {
			return Receipt{}, fault("not_authorized", "thread owner or controller authority required")
		}
		if t.Closed {
			if tool != "close_thread" {
				return Receipt{}, fault("thread_closed", "thread is closed")
			}
			return Receipt{ThreadID: t.ID, State: "closed"}, nil
		}
		switch tool {
		case "close_thread":
			if args.Reason == "" {
				return Receipt{}, fault("invalid_argument", "closure reason is required")
			}
			var pending bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_requests WHERE thread_id=$1 AND state='pending')`, t.ID).Scan(&pending)
			if err != nil {
				return Receipt{}, err
			}
			if pending {
				return Receipt{}, fault("input_pending", "resolve pending input before closing")
			}
			_, err = tx.Exec(ctx, `UPDATE factory.comms_threads SET closed=true WHERE id=$1`, t.ID)
		case "add_participant":
			var exists bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_participants WHERE id=$1 AND execution_id=$2 AND active)`, args.ParticipantID, p.ExecutionID).Scan(&exists)
			if err != nil {
				return Receipt{}, err
			}
			if !exists {
				return Receipt{}, fault("not_authorized", "unknown participant")
			}
			_, err = tx.Exec(ctx, `INSERT INTO factory.comms_members(thread_id,participant_id,joined_sequence) SELECT id,$2,next_sequence FROM factory.comms_threads WHERE id=$1 ON CONFLICT(thread_id,participant_id) DO UPDATE SET removed=false,joined_sequence=EXCLUDED.joined_sequence`, t.ID, args.ParticipantID)
		case "remove_participants":
			if len(args.ParticipantIDs) == 0 {
				return Receipt{}, fault("invalid_argument", "participant_ids is required")
			}
			for _, id := range args.ParticipantIDs {
				var pending bool
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM factory.comms_requests WHERE thread_id=$1 AND owner=$2 AND state='pending')`, t.ID, id).Scan(&pending)
				if err != nil {
					return Receipt{}, err
				}
				if pending {
					return Receipt{}, fault("input_pending", "participant owns pending input")
				}
				_, err = tx.Exec(ctx, `UPDATE factory.comms_members SET removed=true WHERE thread_id=$1 AND participant_id=$2`, t.ID, id)
				if err != nil {
					return Receipt{}, err
				}
			}
		default:
			return Receipt{}, fault("invalid_argument", "unknown membership operation")
		}
		if err != nil {
			return Receipt{}, err
		}
		state := "updated"
		if tool == "close_thread" {
			state = "closed"
		}
		return Receipt{ThreadID: t.ID, State: state}, nil
	})
}
func (s *Service) Participants(ctx context.Context, p Participant, thread string) ([]Participant, error) {
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, `SELECT active FROM factory.comms_participants WHERE id=$1`, p.ID).Scan(&active); err != nil || !active {
		return nil, fault("attempt_inactive", "participant is inactive")
	}
	if _, err = member(ctx, tx, p, thread, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.execution_id,p.name,p.kind,p.active,m.removed,COALESCE(p.work->'step'->>'instructions','') FROM factory.comms_members m JOIN factory.comms_participants p ON p.id=m.participant_id WHERE m.thread_id=$1 ORDER BY p.name,p.id`, thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Participant{}
	for rows.Next() {
		var v Participant
		if err = rows.Scan(&v.ID, &v.ExecutionID, &v.Name, &v.Kind, &v.Active, &v.Removed, &v.Assignment); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// Operator is used only by the private Unix-socket API after resolving an execution.
func (s *Service) Operator(executionID string) Participant {
	return Participant{ID: identity(executionID + "/human"), ExecutionID: executionID, Name: "human", Kind: "human", Active: true}
}
func (s *Service) Threads(ctx context.Context, executionID string) ([]Thread, error) {
	rows, err := s.DB.Pool.Query(ctx, `SELECT id,execution_id,title,context,closed FROM factory.comms_threads WHERE execution_id=$1 ORDER BY created_at,id`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		var t Thread
		var raw []byte
		if err = rows.Scan(&t.ID, &t.ExecutionID, &t.Title, &raw, &t.Closed); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &t.Context); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func appendMessageActivity(ctx context.Context, tx pgx.Tx, p Participant, m Message) error {
	raw, _ := json.Marshal(m)
	return state.AppendActivityTx(ctx, tx, state.Activity{ExecutionID: p.ExecutionID, Source: "message/" + p.Name, Data: append(raw, '\n'), At: m.At})
}
