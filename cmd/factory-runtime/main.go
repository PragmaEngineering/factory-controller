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
	listen := flag.String("listen", "", "loopback HTTP origin address; use HTTPS proxy for remote access")
	publicURL := flag.String("public-url", "", "externally reachable HTTPS origin URL")
	tokenFile := flag.String("token-file", "", "host API bearer credential file")
	flag.Parse()
	if *listen != "" {
		os.Setenv("FACTORY_RUNTIME_LISTEN", *listen)
		os.Setenv("FACTORY_RUNTIME_PUBLIC_URL", *publicURL)
		os.Setenv("FACTORY_RUNTIME_TOKEN_FILE", *tokenFile)
	}
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
