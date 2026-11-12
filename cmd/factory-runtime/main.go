// factory-runtime is the foreground, workflow-free communication/session host.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"controls/internal/runtimehost"
)

func main() {
	root := flag.String("root", "", "private runtime directory (required; must differ from a running Factory runtime)")
	local := flag.Bool("local-database", false, "manage private PostgreSQL under the runtime directory")
	flag.Parse()
	if *root == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: factory-runtime --root DIRECTORY [--local-database]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtimehost.ServeLocal(ctx, *root, *local); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
