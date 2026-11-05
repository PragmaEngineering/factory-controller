package localresources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Inspect uses the OS process start time and group as well as PID and user.
// Commands/arguments are deliberately not persisted: they can contain secrets
// and can change when a launcher execs its actual agent.
func Inspect(pid int) (*Process, error) {
	if pid <= 1 {
		return nil, errors.New("invalid managed process PID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "uid=,pgid=,stat=,lstart=").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(output) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	fields := strings.Fields(string(output))
	if len(fields) != 8 {
		return nil, fmt.Errorf("invalid process identity for %d", pid)
	}
	if strings.HasPrefix(fields[2], "Z") {
		return nil, nil
	}
	uid, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, err
	}
	group, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, err
	}
	return &Process{PID: pid, UID: uid, Group: group, Started: strings.Join(fields[3:], " ")}, nil
}

func Matches(expected *Process) (bool, error) {
	if expected == nil {
		return false, errors.New("missing managed process identity")
	}
	actual, err := Inspect(expected.PID)
	return actual != nil && *actual == *expected && expected.UID == os.Getuid(), err
}

func StopProcess(ctx context.Context, expected *Process, group bool) error {
	live, err := Matches(expected)
	if err != nil || !live {
		return err
	}
	target := expected.PID
	if group {
		if expected.Group != expected.PID {
			return errors.New("managed agent is not its process group leader")
		}
		target = -expected.Group
	}
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		live, err = Matches(expected)
		if err != nil || !live {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for process %d: %w", expected.PID, ctx.Err())
		case <-ticker.C:
		}
	}
}
