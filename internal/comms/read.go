package comms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Service) cursor(p Participant, thread string, sequence int64) string {
	body := fmt.Sprintf("%s/%s/%d", p.ID, thread, sequence)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString([]byte(body)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Service) sequence(p Participant, thread, token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, fault("invalid_argument", "invalid cursor")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, fault("invalid_argument", "invalid cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fault("invalid_argument", "invalid cursor")
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(body)
	prefix := p.ID + "/" + thread + "/"
	if !hmac.Equal(signature, mac.Sum(nil)) || !strings.HasPrefix(string(body), prefix) {
		return 0, fault("not_authorized", "cursor belongs to another participant or thread")
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(string(body), prefix), 10, 64)
	if err != nil || n < 0 {
		return 0, fault("invalid_argument", "invalid cursor sequence")
	}
	return n, nil
}
func (s *Service) Read(ctx context.Context, p Participant, args Read) (Batch, error) {
	out := Batch{Messages: []Message{}}
	if args.Mode != "" && args.Mode != "all" && args.Mode != "unread" {
		return out, fault("invalid_argument", "mode must be all or unread")
	}
	if args.Limit == 0 {
		args.Limit = 50
	}
	if args.Limit < 1 || args.Limit > 200 {
		return out, fault("invalid_argument", "limit must be 1..200")
	}
	after, err := s.sequence(p, args.ThreadID, args.After)
	if err != nil {
		return out, err
	}
	ack, err := s.sequence(p, args.ThreadID, args.AckThrough)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, `SELECT active FROM factory.comms_participants WHERE id=$1`, p.ID).Scan(&active); err != nil || !active {
		return out, fault("attempt_inactive", "participant is inactive")
	}
	t, err := member(ctx, tx, p, args.ThreadID, false)
	if err != nil {
		return out, err
	}
	out.Closed = t.Closed
	out.Thread = t
	if ack > 0 {
		_, err = tx.Exec(ctx, `UPDATE factory.comms_members SET ack_sequence=GREATEST(ack_sequence,$3) WHERE thread_id=$1 AND participant_id=$2`, args.ThreadID, p.ID, ack)
		if err != nil {
			return out, err
		}
	}
	if args.Mode == "unread" {
		var last int64
		if err = tx.QueryRow(ctx, `SELECT ack_sequence FROM factory.comms_members WHERE thread_id=$1 AND participant_id=$2`, args.ThreadID, p.ID).Scan(&last); err != nil {
			return out, err
		}
		if last > after {
			after = last
		}
	}
	rows, err := tx.Query(ctx, `SELECT m.id,m.sequence,m.sender,p.name,m.audience,m.in_reply_to,m.payload,m.created_at
 FROM factory.comms_messages m JOIN factory.comms_members b ON b.thread_id=m.thread_id JOIN factory.comms_participants p ON p.id=m.sender
 WHERE m.thread_id=$1 AND b.participant_id=$2 AND m.sequence>GREATEST($3,b.joined_sequence)
 AND ($2=ANY(m.audience) OR m.sender=$2 OR $4)
 ORDER BY m.sequence LIMIT $5`, args.ThreadID, p.ID, after, p.Kind == "human" || p.Kind == "controller", args.Limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m Message
		var raw []byte
		m.ThreadID = args.ThreadID
		if err = rows.Scan(&m.ID, &m.Sequence, &m.Sender, &m.SenderName, &m.Audience, &m.InReplyTo, &raw, &m.At); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal(raw, &m.Payload); err != nil {
			rows.Close()
			return out, err
		}
		out.Messages = append(out.Messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Messages) > args.Limit {
		out.More = true
		out.Messages = out.Messages[:args.Limit]
	}
	if len(out.Messages) > 0 {
		after = out.Messages[len(out.Messages)-1].Sequence
	}
	out.Cursor = s.cursor(p, args.ThreadID, after)
	return out, tx.Commit(ctx)
}

// Wait is a bounded database-backed wait. Wake-ups are an optimization; the
// periodic recheck recovers missed notifications and listener reconnections.
func (s *Service) Wait(ctx context.Context, p Participant, args Read) (Batch, error) {
	if args.WaitSeconds == 0 {
		args.WaitSeconds = 20
	}
	if args.WaitSeconds < 0 || args.WaitSeconds > 30 {
		return Batch{}, fault("invalid_argument", "wait_seconds must be 0..30")
	}
	deadline := time.NewTimer(time.Duration(args.WaitSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		wake := s.wakeChannel()
		batch, err := s.Read(ctx, p, args)
		if err != nil || len(batch.Messages) > 0 || batch.Closed {
			return batch, err
		}
		select {
		case <-ctx.Done():
			return Batch{}, ctx.Err()
		case <-deadline.C:
			return batch, nil
		case <-ticker.C:
		case <-wake:
		}
	}
}
