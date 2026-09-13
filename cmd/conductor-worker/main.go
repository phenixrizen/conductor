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

	"github.com/phenixrizen/conductor/internal/codegraph"
	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/temporalconnection"
	"go.temporal.io/sdk/client"
)

type config struct {
	databaseURL, address, namespace, credentialsFile string
	temporal                                         temporalconnection.Config
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE"), credentialsFile: getenv("CONDUCTOR_CONTEXT_CREDENTIALS_FILE")}
	if c.databaseURL == "" || !filepath.IsAbs(c.credentialsFile) || len(c.credentialsFile) > 4096 {
		return c, errors.New("DATABASE_URL and an absolute CONDUCTOR_CONTEXT_CREDENTIALS_FILE are required")
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
// available through collection observations; SDK diagnostics use fixed labels.
type safeLogger struct{}

func (safeLogger) Debug(string, ...interface{}) {}
func (safeLogger) Info(string, ...interface{})  {}
func (safeLogger) Warn(string, ...interface{}) {
	log.Print("Temporal worker warning; inspect collection observations")
}
func (safeLogger) Error(string, ...interface{}) {
	log.Print("Temporal worker error; inspect collection observations")
}

func run() error {
	c, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	credentials, err := collectionworker.LoadCredentials(c.credentialsFile)
	if err != nil {
		return errors.New("worker credential references are invalid or unavailable")
	}
	db, err := store.Open(startup, c.databaseURL)
	if err != nil {
		return errors.New("worker PostgreSQL connection is unavailable")
	}
	defer db.Close()
	if err := db.CheckContextSchema(startup); err != nil {
		return err
	}
	engine, err := client.DialContext(startup, c.temporal.Options("conductor-context-worker-v1", safeLogger{}))
	if err != nil {
		return errors.New("Temporal service is unavailable")
	}
	defer engine.Close()
	resolveTarget := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, c.namespace, c.address, contextworkflow.TaskQueue)
	}
	target, err := resolveTarget(startup)
	if err != nil {
		return errors.New("Temporal cluster identity, namespace or retention is unavailable or unsupported")
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, c.namespace, c.address, contextworkflow.TaskQueue, target)
	if err != nil {
		return err
	}
	activity, err := collectionworker.NewActivity(db, credentials.Resolve, nil)
	if err != nil {
		return err
	}
	if image := os.Getenv("CONDUCTOR_CODEGRAPH_IMAGE"); image != "" {
		docker := os.Getenv("CONDUCTOR_DOCKER_BINARY")
		if docker == "" {
			docker = "/usr/bin/docker"
		}
		adapter, e := codegraph.New(docker, image)
		if e != nil {
			return errors.New("CodeGraph requires an immutable image ID and absolute Docker binary")
		}
		activity = activity.WithCodeGraph(adapter)
	}
	dispatcher, err := collectionworker.NewDispatcher(db, runtime, c.namespace, target, resolveTarget)
	if err != nil {
		return err
	}
	active, stopActive := context.WithCancel(ctx)
	defer stopActive()
	done := make(chan error, 2)
	go func() { done <- contextworkflow.RunWorker(active, engine, contextworkflow.TaskQueue, activity.Collect) }()
	go func() {
		done <- dispatcher.Run(active, func(status string) { log.Print("collection dispatcher: ", status) })
	}()
	err = <-done
	stopActive()
	other := <-done
	if err != nil || other != nil {
		return errors.New("collection worker stopped after a runtime failure")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
