package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"controls/internal/app"
	"controls/internal/cli"
	"controls/internal/control"
	"controls/internal/sessions"
)

func main() {
	if err := run(); err != nil {
		if len(os.Args) < 2 || os.Args[1] != "_serve" {
			err = cli.PublicError(err)
		}
		fmt.Fprintln(os.Stderr, "factory:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := sessions.RuntimeRoot()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCommand(ctx, os.Args[1:], root, os.Stdin, os.Stdout, os.Stderr)
}

func runCommand(ctx context.Context, args []string, root string, input io.Reader, output, diagnostics io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cli.Help(output)
		return nil
	}
	controller := control.NewClient(sessions.SocketPath(root))
	switch args[0] {
	case "_serve":
		if len(args) != 1 {
			return fmt.Errorf("usage: factory _serve")
		}
		lease, err := acquireServiceLease(root)
		if err != nil {
			return err
		}
		defer lease.Close()
		return app.Serve(ctx, root)
	case "start":
		if len(args) != 1 {
			return fmt.Errorf("usage: factory start")
		}
		if err := startService(ctx, root, controller, diagnostics); err != nil {
			return err
		}
		fmt.Fprintln(output, "Factory service is running.")
		return nil
	case "stop":
		if len(args) != 1 {
			return fmt.Errorf("usage: factory stop")
		}
		if err := stopService(ctx, root, controller); err != nil {
			return err
		}
		fmt.Fprintln(output, "Factory service is stopped.")
		return nil
	}
	if err := requireService(ctx, root, controller); err != nil {
		return err
	}
	return cli.Run(ctx, args, controller, sessions.NewClient(sessions.SocketPath(root)), input, output)
}
