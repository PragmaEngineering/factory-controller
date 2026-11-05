package localresources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// EmbeddedDatabasePath mirrors the pinned embedded backend's per-cwd layout.
// Keep this compatibility boundary here; never scan unrelated databases.
func EmbeddedDatabasePath(root string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(home, ".hatchet-embedded", hex.EncodeToString(sum[:6])), nil
}

func DatabaseProcess(path string) (*Process, error) {
	if err := SafePath(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(path, "data", "postmaster.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 {
		return nil, errors.New("invalid managed database lock file")
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return nil, err
	}
	process, err := Inspect(pid)
	if err != nil || process == nil {
		return process, err
	}
	// Verify the executable and exact data argument, not just the lock-file PID.
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return nil, err
	}
	expected := filepath.Join(path, "runtime", "bin", "postgres") + " -D " + filepath.Join(path, "data") + " "
	if process.UID != os.Getuid() || !strings.HasPrefix(strings.TrimSpace(string(output)), expected) {
		return nil, fmt.Errorf("database PID %d does not match its managed executable and data directory", pid)
	}
	return process, nil
}

func StopDatabase(ctx context.Context, path string) error {
	process, err := DatabaseProcess(path)
	if err != nil || process == nil {
		return err
	}
	// pg_ctl performs database-aware shutdown, checkpointing before exit. Never
	// remove postmaster.pid, and never signal a database identified only by PID.
	command := exec.CommandContext(ctx, filepath.Join(path, "runtime", "bin", "pg_ctl"), "stop", "-w", "-t", "30", "-m", "fast", "-D", filepath.Join(path, "data"))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("stop managed database: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
