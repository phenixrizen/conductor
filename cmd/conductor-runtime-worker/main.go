package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/runtimeworker"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/temporalconnection"
	"go.temporal.io/sdk/client"
)

type config struct {
	databaseURL, address, namespace, credentialsFile string
	temporal                                         temporalconnection.Config
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE"), credentialsFile: getenv("CONDUCTOR_RUNTIME_CREDENTIALS_FILE")}
	if c.databaseURL == "" || !filepath.IsAbs(c.credentialsFile) || len(c.credentialsFile) > 4096 {
		return c, errors.New("DATABASE_URL and an absolute CONDUCTOR_RUNTIME_CREDENTIALS_FILE are required")
	}
	var err error
	c.temporal, err = temporalconnection.Load(getenv)
	if err != nil {
		return c, err
	}
	c.address, c.namespace = c.temporal.Address, c.temporal.Namespace
	return c, nil
}

// SDK fields and error causes are not safe log content. Operational progress is
// available through runtime evidence observations; SDK diagnostics use fixed labels.
type safeLogger struct{}

func (safeLogger) Debug(string, ...interface{}) {}
func (safeLogger) Info(string, ...interface{})  {}
func (safeLogger) Warn(string, ...interface{}) {
	log.Print("Temporal worker warning; inspect runtime evidence observations")
}
func (safeLogger) Error(string, ...interface{}) {
	log.Print("Temporal worker error; inspect runtime evidence observations")
}

func run() error {
	if len(os.Args) > 1 {
		if os.Args[1] == "configure" {
			return configure(os.Args[2:])
		}
		return errors.New("unknown runtime evidence worker command")
	}
	c, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	credentials, err := runtimeworker.LoadCredentials(c.credentialsFile)
	if err != nil {
		return errors.New("worker credential references are invalid or unavailable")
	}
	db, err := store.Open(startup, c.databaseURL)
	if err != nil {
		return errors.New("worker PostgreSQL connection is unavailable")
	}
	defer db.Close()
	if err := db.CheckRuntimeSchema(startup); err != nil {
		return err
	}
	engine, err := client.DialContext(startup, c.temporal.Options("conductor-runtime-worker-v1", safeLogger{}))
	if err != nil {
		return errors.New("Temporal service is unavailable")
	}
	defer engine.Close()
	resolveTarget := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, c.namespace, c.address, runtimeworker.TaskQueue)
	}
	target, err := resolveTarget(startup)
	if err != nil {
		return errors.New("Temporal cluster identity, namespace or retention is unavailable or unsupported")
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, c.namespace, c.address, runtimeworker.TaskQueue, target)
	if err != nil {
		return err
	}
	runtime, err = runtime.ForWorkflow(runtimeworker.WorkflowName)
	if err != nil {
		return err
	}
	activity, err := runtimeworker.NewActivity(db, credentials.Resolve)
	if err != nil {
		return err
	}
	dispatcher, err := runtimeworker.NewDispatcher(db, runtime, c.namespace, target, resolveTarget)
	if err != nil {
		return err
	}
	active, stopActive := context.WithCancel(ctx)
	defer stopActive()
	done := make(chan error, 2)
	go func() { done <- runtimeworker.RunWorker(active, engine, activity.Collect) }()
	go func() {
		done <- dispatcher.Run(active, func(status string) { log.Print("runtime evidence dispatcher: ", status) })
	}()
	err = <-done
	stopActive()
	other := <-done
	if err != nil || other != nil {
		return errors.New("runtime evidence worker stopped after a runtime failure")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
