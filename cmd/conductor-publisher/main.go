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

	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/deliveryworker"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/temporalconnection"
	"go.temporal.io/sdk/client"
)

type config struct {
	databaseURL, address, namespace, credentialsFile string
	temporal                                         temporalconnection.Config
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE"), credentialsFile: getenv("CONDUCTOR_PUBLICATION_CREDENTIALS_FILE")}
	if c.databaseURL == "" || !filepath.IsAbs(c.credentialsFile) || len(c.credentialsFile) > 4096 {
		return c, errors.New("DATABASE_URL and an absolute CONDUCTOR_PUBLICATION_CREDENTIALS_FILE are required")
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
// available through publication observations; SDK diagnostics use fixed labels.
type safeLogger struct{}

func (safeLogger) Debug(string, ...interface{}) {}
func (safeLogger) Info(string, ...interface{})  {}
func (safeLogger) Warn(string, ...interface{}) {
	log.Print("Temporal worker warning; inspect publication observations")
}
func (safeLogger) Error(string, ...interface{}) {
	log.Print("Temporal worker error; inspect publication observations")
}

func run() error {
	if len(os.Args) > 1 {
		if os.Args[1] == "configure" {
			return configure(os.Args[2:])
		}
		return errors.New("unknown publication worker command")
	}
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
	if err := db.CheckDeliverySchema(startup); err != nil {
		return err
	}
	engine, err := client.DialContext(startup, c.temporal.Options("conductor-publication-worker-v1", safeLogger{}))
	if err != nil {
		return errors.New("Temporal service is unavailable")
	}
	defer engine.Close()
	resolveTarget := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, c.namespace, c.address, deliveryworker.TaskQueue)
	}
	target, err := resolveTarget(startup)
	if err != nil {
		return errors.New("Temporal cluster identity, namespace or retention is unavailable or unsupported")
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, c.namespace, c.address, deliveryworker.TaskQueue, target)
	if err != nil {
		return err
	}
	runtime, err = runtime.ForWorkflow(deliveryworker.WorkflowName)
	if err != nil {
		return err
	}
	source := db.LoadDeliverySource
	resolveCredential := func(ctx context.Context, t domain.DeliveryTarget, id string) (string, error) {
		return credentials.Resolve(ctx, domain.ContextIntegration{CredentialID: id, Source: domain.ContextSource{WorkspaceID: t.WorkspaceID, RepositoryID: t.RepositoryID, Provider: t.Provider, Host: t.Host, ProviderID: t.ProviderID}})
	}
	activity, err := deliveryworker.NewActivity(db, source, resolveCredential)
	if err != nil {
		return err
	}
	dispatcher, err := deliveryworker.NewDispatcher(db, runtime, c.namespace, target, resolveTarget)
	if err != nil {
		return err
	}
	active, stopActive := context.WithCancel(ctx)
	defer stopActive()
	done := make(chan error, 2)
	go func() { done <- deliveryworker.RunWorker(active, engine, activity.Publish) }()
	go func() {
		done <- dispatcher.Run(active, func(status string) { log.Print("publication dispatcher: ", status) })
	}()
	err = <-done
	stopActive()
	other := <-done
	if err != nil || other != nil {
		return errors.New("publication worker stopped after a runtime failure")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
