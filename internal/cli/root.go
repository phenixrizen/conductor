// Package cli parses command-line flags and dispatches subcommands. It holds
// no business logic.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/phenixrizen/conductor/internal/version"
)

const usage = `conductor - shared agent terminals

Usage:
  conductor serve [flags]      run the web server
  conductor host [flags] -- <command...>
                               host a local terminal session
  conductor notify [flags]     report "needs input" from inside a session
  conductor version            print the version

Run "conductor <command> -h" for command flags.
`

// Run executes the CLI and returns the process exit code.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2, nil
	}
	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "host":
		return runHost(ctx, args[1:], stdin, stdout, stderr)
	case "notify":
		return runNotify(ctx, args[1:], stdin, stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "conductor", version.String())
		return 0, nil
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0, nil
	}
	fmt.Fprint(stderr, usage)
	return 2, fmt.Errorf("unknown command %q", args[0])
}
