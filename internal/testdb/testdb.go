package testdb

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"

	"controls/internal/state"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Start launches a private, empty PostgreSQL instance. State.Open applies its
// migrations separately so tests can exercise concurrent first startup.
func Start(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint32(listener.Addr().(*net.TCPAddr).Port)
	_ = listener.Close()
	root := t.TempDir()
	server := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(port).Database("factory_test").
		RuntimePath(filepath.Join(root, "runtime")).
		DataPath(filepath.Join(root, "data")).Logger(io.Discard))
	if err := server.Start(); err != nil {
		t.Fatalf("start test PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	t.Setenv("FACTORY_DATABASE_URL", fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/factory_test?sslmode=disable", port))
}

func Open(t *testing.T) *state.Store {
	t.Helper()
	Start(t)
	db, err := state.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}
