package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/phenixrizen/conductor/internal/designtools"
)

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: conductor-design-tools spec-init | spec-template | spec-check | adr-lint | adr-check | adr-explain | adr-graph | adr-new")
		return 2
	}
	in := designtools.Request{Command: os.Args[1]}
	flags := flag.NewFlagSet("conductor-design-tools", flag.ContinueOnError)
	flags.StringVar(&in.Feature, "feature", "", "exact specs/<feature> directory")
	flags.StringVar(&in.Kind, "kind", "", "spec, plan, tasks or checklist core template")
	flags.StringVar(&in.Directory, "dir", "", "ADR corpus directory")
	flags.StringVar(&in.Title, "title", "", "new Proposed ADR title")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return 2
	}
	in.Paths = flags.Args()
	if strings.HasPrefix(in.Command, "adr-") && in.Directory == "" {
		in.Directory = "docs/adr"
	}
	root, err := os.Getwd()
	if err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := designtools.DefaultRuntime().Execute(ctx, root, in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "design tool unavailable or input blocked")
		if report.Command == "" {
			return 2
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(report)
	if err != nil || report.State != "passed" && report.State != "produced" {
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
