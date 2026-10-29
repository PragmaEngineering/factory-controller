package comms

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

func (s *Service) Input(ctx context.Context, id string) (*Request, error) {
	var r Request
	err := s.DB.Pool.QueryRow(ctx, `SELECT id,execution_id,thread_id,owner,prompt,options,state,created_at FROM factory.comms_requests WHERE id=$1`, id).Scan(&r.ID, &r.ExecutionID, &r.ThreadID, &r.Owner, &r.Prompt, &r.Options, &r.State, &r.At)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &r, err
}

// AuditMessages is exposed only on the private operator API. The native MCP
// interface always filters reads through the authenticated participant scope.
func (s *Service) AuditMessages(ctx context.Context, executionID string) ([]Message, error) {
	rows, err := s.DB.Pool.Query(ctx, `SELECT m.id,m.thread_id,m.sequence,m.sender,p.name,m.audience,m.in_reply_to,m.payload,m.created_at,ARRAY(SELECT name FROM factory.comms_participants WHERE id=ANY(m.audience) ORDER BY name) FROM factory.comms_messages m JOIN factory.comms_threads t ON t.id=m.thread_id JOIN factory.comms_participants p ON p.id=m.sender WHERE t.execution_id=$1 ORDER BY m.created_at DESC,m.id DESC LIMIT 500`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var raw []byte
		if err = rows.Scan(&m.ID, &m.ThreadID, &m.Sequence, &m.Sender, &m.SenderName, &m.Audience, &m.InReplyTo, &raw, &m.At, &m.RecipientNames); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &m.Payload); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
