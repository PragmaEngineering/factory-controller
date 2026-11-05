package localresources

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	command := exec.Command("sleep", "60")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, command.Start())
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-done })
	return command
}

func TestRecoveryStopsRegisteredGroupWithoutDatabase(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	registry := Registry{Root: root}
	command := startSleeper(t)
	unrelated := startSleeper(t)
	require.NoError(t, registry.RecordProcess("agent", command.Process.Pid))
	items, err := registry.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, StopProcess(ctx, items[0].Process, true))
	process, err := Inspect(command.Process.Pid)
	require.NoError(t, err)
	require.Nil(t, process)
	process, err = Inspect(unrelated.Process.Pid)
	require.NoError(t, err)
	require.NotNil(t, process)
}

func TestPIDReuseIdentityDoesNotSignalProcess(t *testing.T) {
	command := startSleeper(t)
	process, err := Inspect(command.Process.Pid)
	require.NoError(t, err)
	process.Started = "different process lifetime"
	require.NoError(t, StopProcess(context.Background(), process, true))
	live, err := Inspect(command.Process.Pid)
	require.NoError(t, err)
	require.NotNil(t, live)
}

func TestDatabaseLockCannotIdentifyUnrelatedProcess(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	command := startSleeper(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "data"), 0700))
	pid := command.Process.Pid
	require.NoError(t, os.WriteFile(filepath.Join(root, "data", "postmaster.pid"), []byte(strconv.Itoa(pid)+"\n"+filepath.Join(root, "data")+"\n0\n"), 0600))
	require.ErrorContains(t, StopDatabase(context.Background(), root), "does not match")
	live, err := Inspect(pid)
	require.NoError(t, err)
	require.NotNil(t, live)
}

func TestRecordsRejectSymlinkPathsAndCorruption(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	registry := Registry{Root: root}
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, "link")))
	require.ErrorContains(t, registry.RecordPath("retained", filepath.Join(root, "link", "files")), "symlink")
	require.NoError(t, registry.RecordPath("retained", filepath.Join(root, "files")))
	items, err := registry.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, os.WriteFile(registry.filename(items[0]), []byte("{"), 0600))
	_, err = registry.List()
	require.Error(t, err)
}

func TestPrunePreservesHistorySourceAndSymlinkTargets(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	source := t.TempDir()
	sourceFile := filepath.Join(source, "main.go")
	require.NoError(t, os.WriteFile(sourceFile, []byte("source"), 0600))
	history := filepath.Join(root, "history")
	profiles := filepath.Join(history, "profiles")
	require.NoError(t, os.MkdirAll(profiles, 0700))
	require.NoError(t, os.Symlink(source, filepath.Join(profiles, "source-link")))
	evidence := filepath.Join(history, "review.html")
	require.NoError(t, os.WriteFile(evidence, []byte("review"), 0600))
	registry := Registry{Root: root}
	require.NoError(t, registry.RecordPath("retained", history))
	require.NoError(t, registry.RecordPath("disposable", profiles))
	plan, err := registry.PlanPrune(false)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.DirExists(t, profiles) // Planning is non-mutating.
	require.NoError(t, registry.ApplyPrune(plan))
	require.NoDirExists(t, profiles)
	require.FileExists(t, evidence)
	require.FileExists(t, sourceFile)
}

func TestPruneRefusesLiveAgentsAndRedirectedPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	registry := Registry{Root: root}
	command := startSleeper(t)
	require.NoError(t, registry.RecordProcess("agent", command.Process.Pid))
	_, err = registry.PlanPrune(false)
	require.ErrorContains(t, err, "is running")
	items, err := registry.List()
	require.NoError(t, err)
	require.NoError(t, registry.Forget(items[0]))
	profiles := filepath.Join(root, "profiles")
	require.NoError(t, os.Mkdir(profiles, 0700))
	require.NoError(t, registry.RecordPath("disposable", profiles))
	require.NoError(t, os.Remove(profiles))
	require.NoError(t, os.Symlink(t.TempDir(), profiles))
	_, err = registry.PlanPrune(false)
	require.ErrorContains(t, err, "symlink")
}

func TestPruneRefusesReplacedDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	profiles := filepath.Join(root, "profiles")
	require.NoError(t, os.Mkdir(profiles, 0700))
	registry := Registry{Root: root}
	require.NoError(t, registry.RecordPath("disposable", profiles))
	require.NoError(t, os.Rename(profiles, profiles+"-original"))
	require.NoError(t, os.Mkdir(profiles, 0700))
	_, err = registry.PlanPrune(false)
	require.ErrorContains(t, err, "identity changed")
	require.DirExists(t, profiles)
}
