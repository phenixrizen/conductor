// conductor-sandbox is a disposable worker-image entry point. Do not run it as a
// host command: its execution package requires an unprivileged container PID 1.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/phenixrizen/conductor/internal/execution"
)

func main() {
	var err error
	switch {
	case len(os.Args) == 2 && os.Args[1] == "provider-proxy":
		err = execution.ProxyMain(context.Background(), os.Stdin)
	case len(os.Args) == 2 && os.Args[1] == "proxy-ready":
		err = execution.ProxyReady()
	case len(os.Args) == 1:
		err = execution.SandboxMain(context.Background(), os.Stdin, os.Stdout)
	default:
		os.Exit(2)
	}
	if err != nil {
		// Source, credentials and raw assistant output must not become supervisor
		// errors or Temporal history. Detailed command evidence is a bounded result.
		fmt.Fprintln(os.Stderr, "Conductor sandbox did not complete:", err)
		os.Exit(1)
	}
}
