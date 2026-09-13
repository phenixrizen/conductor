package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

func configure(args []string) error {
	flags := flag.NewFlagSet("configure", flag.ContinueOnError)
	file := flags.String("file", "", "operator-controlled integration JSON file")
	operator := flags.String("operator", "", "bounded operator audit label")
	check := flags.Bool("check", false, "validate JSON without applying it")
	if flags.Parse(args) != nil || flags.NArg() != 0 || domain.ValidateAccessID(*operator) != nil || *file == "" {
		return errors.New("usage: conductor-publisher configure --file integrations.json --operator label [--check]")
	}
	f, err := os.OpenFile(*file, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("publication configuration unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("publication configuration must be a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("publication configuration exceeds its bound")
	}
	var config struct {
		Integrations []domain.DeliveryIntegrationConfig `json:"integrations"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Integrations) < 1 || len(config.Integrations) > 128 {
		return errors.New("invalid publication configuration")
	}
	seen := map[string]bool{}
	for _, entry := range config.Integrations {
		if domain.ValidateDeliveryIntegration(entry) != nil || seen[entry.RepositoryID] {
			return errors.New("invalid publication integration")
		}
		seen[entry.RepositoryID] = true
	}
	if *check {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("publication database unavailable")
	}
	defer db.Close()
	if err = db.ApplyDeliveryIntegrations(ctx, *operator, config.Integrations); err != nil {
		return errors.New("publication configuration failed; transaction rolled back")
	}
	return nil
}
