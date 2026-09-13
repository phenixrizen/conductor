// conductor-tracker-admin provisions workspace trackers through a trusted database operator connection.
// It does not expose an HTTP administration endpoint or grant package approvals.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

func readConfig(path string) (domain.TrackerConfig, error) {
	var config domain.TrackerConfig
	if path == "" || path == "-" {
		return config, errors.New("--file must select an tracker configuration JSON file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return config, fmt.Errorf("open tracker configuration: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return config, errors.New("tracker configuration must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return config, fmt.Errorf("read tracker configuration: %w", err)
	}
	if len(data) > 1<<20 {
		return config, errors.New("tracker configuration exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("decode tracker configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return config, errors.New("tracker configuration must contain exactly one JSON object")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return config, errors.New("tracker configuration must be an object")
	}
	return config, domain.ValidateTrackerConfig(config)
}

func run(args []string, databaseURL string, output io.Writer) error {
	if len(args) == 0 || args[0] != "apply" {
		return errors.New("usage: conductor-tracker-admin apply --file tracker.json --operator <operator-label> [--check]")
	}
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(output)
	file := flags.String("file", "", "explicit tracker configuration JSON file")
	operator := flags.String("operator", "", "operator label retained in the administration audit (not authentication)")
	check := flags.Bool("check", false, "validate file syntax and values without connecting to PostgreSQL")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := domain.ValidateActor(*operator); err != nil {
		return errors.New("--operator must provide a bounded operator label for the audit record")
	}
	config, err := readConfig(*file)
	if err != nil {
		return err
	}
	if *check {
		_, err := fmt.Fprintln(output, "Tracker configuration syntax and values are valid. Database identities and references have not been checked.")
		return err
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required for the trusted operator connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer db.Close()
	if err := db.ApplyTrackerConfig(ctx, *operator, config); err != nil {
		return fmt.Errorf("apply tracker configuration: %w", err)
	}
	_, err = fmt.Fprintln(output, "Tracker configuration and administration audit committed. Omitted records were unchanged.")
	return err
}

func main() {
	if err := run(os.Args[1:], os.Getenv("DATABASE_URL"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
