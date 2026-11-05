package localresources

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/stretchr/testify/require"
)

func TestRecoverAndPrunePrivateDatabase(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint32(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	server := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().Port(port).Database("factory_test").RuntimePath(filepath.Join(root, "runtime")).DataPath(filepath.Join(root, "data")).Logger(io.Discard))
	require.NoError(t, server.Start())
	t.Cleanup(func() { _ = server.Stop() })
	registry := Registry{Root: filepath.Join(root, "owner")}
	require.NoError(t, registry.RecordPath("database", root))
	process, err := DatabaseProcess(root)
	require.NoError(t, err)
	require.NotNil(t, process)
	_, err = registry.PlanPrune(true)
	require.ErrorContains(t, err, "is running")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, StopDatabase(ctx, root))
	require.FileExists(t, filepath.Join(root, "data", "PG_VERSION"))
	plan, err := registry.PlanPrune(false)
	require.NoError(t, err)
	require.Empty(t, plan)
	// Keep the ownership journal outside the database being removed.
	outside := Registry{Root: t.TempDir()}
	require.NoError(t, outside.RecordPath("database", root))
	plan, err = outside.PlanPrune(true)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.NoError(t, outside.ApplyPrune(plan))
	require.NoDirExists(t, root)
}
