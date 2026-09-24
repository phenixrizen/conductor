// Command conductor runs the shared agent terminal server (`serve`) or hosts a
// local terminal session from a developer machine (`host`).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/phenixrizen/conductor/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	code, err := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "conductor:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
