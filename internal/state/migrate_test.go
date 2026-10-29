package state_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"controls/internal/state"
	"controls/internal/testdb"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestMigrationVersionAndRestart(t *testing.T) {
	db := testdb.Open(t)
	assertCurrentVersion(t, db)
	require.NoError(t, db.Create(context.Background(), "fixture", "persisted", map[string]string{"value": "kept"}))

	reopened, err := state.Open(context.Background())
	require.NoError(t, err)
	t.Cleanup(reopened.Close)
	assertCurrentVersion(t, reopened)
	var record map[string]string
	require.NoError(t, reopened.Get(context.Background(), "fixture", "persisted", &record))
	require.Equal(t, "kept", record["value"])
}

func TestMigrationAdoptsExistingFactoryTables(t *testing.T) {
	testdb.Start(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("FACTORY_DATABASE_URL"))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `
		CREATE SCHEMA factory;
		CREATE TABLE factory.records (kind text NOT NULL, id text NOT NULL, content jsonb NOT NULL, PRIMARY KEY (kind,id));
		CREATE TABLE factory.output (session_id text NOT NULL, position bigint GENERATED ALWAYS AS IDENTITY, body bytea NOT NULL, PRIMARY KEY (session_id,position));
		INSERT INTO factory.records(kind,id,content) VALUES ('fixture','old','{"value":"preserved"}');
	`)
	require.NoError(t, err)

	db, err := state.Open(ctx)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	assertCurrentVersion(t, db)
	var record map[string]string
	require.NoError(t, db.Get(ctx, "fixture", "old", &record))
	require.Equal(t, "preserved", record["value"])
}

func TestConcurrentMigrationStartup(t *testing.T) {
	testdb.Start(t)
	var group sync.WaitGroup
	errors := make(chan error, 4)
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			opened, err := state.Open(context.Background())
			if err == nil {
				opened.Close()
			}
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	db, err := state.Open(context.Background())
	require.NoError(t, err)
	t.Cleanup(db.Close)
	assertCurrentVersion(t, db)
}

func assertCurrentVersion(t *testing.T, db *state.Store) {
	t.Helper()
	var version int
	var dirty bool
	err := db.Pool.QueryRow(context.Background(), `SELECT version, dirty FROM public.factory_schema_migrations`).Scan(&version, &dirty)
	require.NoError(t, err)
	require.Equal(t, 4, version)
	require.False(t, dirty)
}
