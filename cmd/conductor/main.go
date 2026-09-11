package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	f := flag.NewFlagSet(cmd, flag.ExitOnError)
	actor := f.String("actor", "", "local development identity")
	title := f.String("title", "", "package title")
	rev := f.Int64("revision", 0, "inspected revision")
	digest := f.String("digest", "", "inspected digest")
	_ = f.Parse(os.Args[2:])
	if *actor == "" {
		fmt.Fprintln(os.Stderr, "--actor is required")
		os.Exit(2)
	}
	c := client.New(env("CONDUCTOR_URL", "http://localhost:8080"), *actor)
	var p domain.Package
	var err error
	args := f.Args()
	switch cmd {
	case "create":
		p, err = c.Create(context.Background(), domain.Content{"intent": map[string]any{"title": *title}})
	case "show":
		need(args)
		p, err = c.Get(context.Background(), args[0])
	case "submit":
		need(args)
		p, err = c.Submit(context.Background(), args[0], *rev)
	case "approve":
		need(args)
		p, err = c.Approve(context.Background(), args[0], *rev, *digest)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(p)
}
func need(a []string) {
	if len(a) == 0 {
		usage()
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: conductor <create|show|submit|approve> [id] --actor NAME [options]")
	os.Exit(2)
}
