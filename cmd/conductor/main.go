package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
	"io"
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
	file := f.String("file", "", "JSON package content file ('-' for stdin)")
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
		content, readErr := contentFrom(*file, *title)
		if readErr != nil {
			err = readErr
			break
		}
		p, err = c.Create(context.Background(), content)
	case "revise":
		need(args)
		content, readErr := contentFrom(*file, *title)
		if readErr != nil {
			err = readErr
			break
		}
		p, err = c.Revise(context.Background(), args[0], *rev, content)
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
	fmt.Fprintln(os.Stderr, "usage: conductor <create|revise|show|submit|approve> [flags] [id]")
	os.Exit(2)
}

func contentFrom(path, title string) (domain.Content, error) {
	if path == "" {
		if title == "" {
			return nil, errors.New("--title or --file is required")
		}
		return domain.Content{"intent": map[string]any{"title": title}}, nil
	}
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	var content domain.Content
	d := json.NewDecoder(io.LimitReader(r, (1<<20)+1))
	if err := d.Decode(&content); err != nil {
		return nil, fmt.Errorf("read package content: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("package file must contain exactly one JSON value of at most 1 MiB")
	}
	return content, nil
}
