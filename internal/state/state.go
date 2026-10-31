package state

import (
	"context"
	"controls/internal/telemetry"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns Factory's records. Hatchet uses the same PostgreSQL installation
// locally, but Factory never depends on Hatchet's private tables.
type Store struct{ Pool *pgxpool.Pool }

// Activity is one durable item in an execution's operator-visible stream.
// Data contains the exact bytes supplied by the source, including newlines.
type Activity struct {
	ID          int64     `json:"id"`
	ExecutionID string    `json:"execution_id"`
	SessionID   string    `json:"session_id,omitempty"`
	Source      string    `json:"source"`
	Data        []byte    `json:"data"`
	At          time.Time `json:"at"`
}

func Open(ctx context.Context) (*Store, error) {
	url := os.Getenv("FACTORY_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL") // supplied by embedded Hatchet
	}
	if url == "" {
		return nil, errors.New("Factory database URL is unavailable")
	}
	if err := migrateSchema(url); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open Factory database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect Factory database: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) Get(ctx context.Context, kind, id string, out any) error {
	var content []byte
	err := s.Pool.QueryRow(ctx, `SELECT content FROM factory.records WHERE kind=$1 AND id=$2`, kind, id).Scan(&content)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, out)
}

func (s *Store) List(ctx context.Context, kind string) ([][]byte, error) {
	rows, err := s.Pool.Query(ctx, `SELECT content FROM factory.records WHERE kind=$1`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records [][]byte
	for rows.Next() {
		var content []byte
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		records = append(records, content)
	}
	return records, rows.Err()
}

func (s *Store) Create(ctx context.Context, kind, id string, value any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ($1,$2,$3)`, kind, id, content)
	return err
}

type Record struct {
	Kind  string
	ID    string
	Value any
}

func (s *Store) CreateTogether(ctx context.Context, records ...Record) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, record := range records {
		content, err := json.Marshal(record.Value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ($1,$2,$3)`, record.Kind, record.ID, content); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AppendActivityTx records activity in the caller's transaction, so an
// execution creation and its first stream entry become visible together.
func AppendActivityTx(ctx context.Context, tx pgx.Tx, activity Activity) error {
	return appendActivity(ctx, tx, activity)
}

// Change serializes updates to one record across manager replicas. The
// callback receives the current JSON and returns the replacement document.
func (s *Store) Change(ctx context.Context, kind, id string, change func([]byte) ([]byte, error), activities ...Activity) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current []byte
	if err := tx.QueryRow(ctx, `SELECT content FROM factory.records WHERE kind=$1 AND id=$2 FOR UPDATE`, kind, id).Scan(&current); err != nil {
		return err
	}
	next, err := change(current)
	if err != nil {
		return err
	}
	if next != nil {
		if _, err := tx.Exec(ctx, `UPDATE factory.records SET content=$3 WHERE kind=$1 AND id=$2`, kind, id, next); err != nil {
			return err
		}
		for _, activity := range activities {
			if err := appendActivity(ctx, tx, activity); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) AppendOutput(ctx context.Context, id string, body []byte, activity Activity) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO factory.output(session_id,body) VALUES ($1,$2)`, id, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO factory.records(kind,id,content) VALUES ('telemetry',$1,'{}'::jsonb) ON CONFLICT DO NOTHING`, id); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT content FROM factory.records WHERE kind='telemetry' AND id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		return err
	}
	var projection telemetry.Projection
	if err := json.Unmarshal(raw, &projection); err != nil {
		return err
	}
	projection.Append(body, time.Now().UTC())
	encoded, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE factory.records SET content=$2 WHERE kind='telemetry' AND id=$1`, id, encoded); err != nil {
		return err
	}
	if activity.ExecutionID != "" {
		activity.Data = body
		if err := appendActivity(ctx, tx, activity); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) AppendActivity(ctx context.Context, activity Activity) error {
	if activity.ExecutionID == "" {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := appendActivity(ctx, tx, activity); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func appendActivity(ctx context.Context, tx pgx.Tx, activity Activity) error {
	if activity.ExecutionID == "" || activity.Source == "" {
		return errors.New("activity needs execution ID and source")
	}
	// Serialize appends for one execution so cursor order matches commit order.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, activity.ExecutionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO factory.activity(execution_id,session_id,source,data) VALUES ($1,$2,$3,$4)`,
		activity.ExecutionID, activity.SessionID, activity.Source, activity.Data)
	return err
}

func (s *Store) Activity(ctx context.Context, executionID string, after int64, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,execution_id,session_id,source,data,at FROM factory.activity
		WHERE execution_id=$1 AND id>$2 ORDER BY id LIMIT $3`, executionID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Activity
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.ID, &item.ExecutionID, &item.SessionID, &item.Source, &item.Data, &item.At); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Output(ctx context.Context, id string) ([]byte, error) {
	rows, err := s.Pool.Query(ctx, `SELECT body FROM factory.output WHERE session_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []byte
	for rows.Next() {
		var chunk []byte
		if err := rows.Scan(&chunk); err != nil {
			return nil, err
		}
		result = append(result, chunk...)
	}
	return result, rows.Err()
}
