package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/phenixrizen/conductor/internal/databaseops"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("use migrate, backup, or restore")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "migrate":
		dir := flags.String("directory", "migrations", "inspected release migration directory")
		baseline := flags.Int("baseline", 0, "explicitly inspected legacy migration count")
		operator := flags.String("operator", "", "declared operator audit label")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		migrations, err := databaseops.ReadMigrations(*dir)
		if err != nil {
			return err
		}
		if err = databaseops.Migrate(ctx, url, migrations, *baseline, *operator); err != nil {
			return err
		}
		fmt.Println("Migrations and release checksums committed.")
		return nil
	case "backup":
		output := flags.String("output", "", "absolute new archive path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		m, err := databaseops.Backup(ctx, url, *output)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "restore":
		input := flags.String("input", "", "absolute inspected archive path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		if err := databaseops.Restore(ctx, url, *input); err != nil {
			return err
		}
		fmt.Println("Archive restored into the empty target. Keep services stopped until Temporal history and runtime bindings are reconciled.")
		return nil
	default:
		return errors.New("use migrate, backup, or restore")
	}
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
