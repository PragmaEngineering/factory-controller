package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"controls/internal/localresources"
	"controls/internal/sessions"
)

func pruneCommand(ctx context.Context, args []string, root string, input io.Reader, output io.Writer) error {
	all, dryRun, yes := false, false, false
	seen := map[string]bool{}
	for _, arg := range args {
		if seen[arg] {
			return errors.New("duplicate prune option")
		}
		seen[arg] = true
		switch arg {
		case "--all":
			all = true
		case "--dry-run":
			dryRun = true
		case "--yes":
			yes = true
		default:
			return errors.New("usage: factory prune [--all] [--dry-run] [--yes]")
		}
	}
	if yes && !all {
		return errors.New("--yes requires --all")
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(output, "No managed resources to prune.")
		return nil
	} else if err != nil {
		return err
	}
	lock, err := lockStartup(ctx, root)
	if err != nil {
		return err
	}
	defer lock.Close()
	lease, err := acquireServiceLease(root)
	if err != nil {
		return errors.New("Factory service is running; run factory stop --all")
	}
	defer lease.Close()
	// Older services may not hold a lease. A live listener is still authoritative.
	socket := sessions.SocketPath(root)
	if err := requireNoServiceListener(root); err != nil {
		return err
	}
	registry := localresources.Registry{Root: root}
	plan, err := registry.PlanPrune(all)
	if err != nil {
		return err
	}
	for _, action := range plan {
		target := action.Path
		if target == "" {
			target = fmt.Sprintf("process record %d", action.Resource.Process.PID)
		}
		fmt.Fprintf(output, "candidate %s: %s\n", action.Resource.Kind, target)
	}
	info, err := os.Lstat(socket)
	removeSocket := err == nil && info.Mode()&os.ModeSocket != 0
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if removeSocket {
		fmt.Fprintf(output, "candidate stale socket: %s\n", socket)
	}
	if dryRun {
		fmt.Fprintln(output, "Dry run; no resources removed.")
		return nil
	}
	if all && !yes && len(plan) > 0 {
		fmt.Fprint(output, "Delete retained execution history, files, and managed database data? Type yes: ")
		if err := confirmPrune(ctx, input); err != nil {
			return err
		}
	}
	if err := registry.ApplyPrune(plan); err != nil {
		return err
	}
	count := len(plan)
	if removeSocket {
		if err := os.Remove(socket); err != nil {
			return err
		}
		count++
	}
	fmt.Fprintf(output, "Pruned %d managed resources.\n", count)
	return nil
}

func confirmPrune(ctx context.Context, input io.Reader) error {
	answer := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		answer <- scanner.Scan() && strings.TrimSpace(scanner.Text()) == "yes"
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case confirmed := <-answer:
		if !confirmed {
			return errors.New("prune cancelled")
		}
		return nil
	}
}
