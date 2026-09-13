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
	"github.com/phenixrizen/conductor/internal/coordinationworker"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/temporalconnection"
	"go.temporal.io/sdk/client"
)

type config struct {
	databaseURL, address, namespace, profilesFile string
	temporal                                      temporalconnection.Config
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE"), profilesFile: getenv("CONDUCTOR_EXECUTION_PROFILES_FILE")}
	if c.databaseURL == "" || !filepath.IsAbs(c.profilesFile) || len(c.profilesFile) > 4096 {
		return c, errors.New("DATABASE_URL and an absolute CONDUCTOR_EXECUTION_PROFILES_FILE are required")
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
// available through coordination observations; SDK diagnostics use fixed labels.
type safeLogger struct{}

func (safeLogger) Debug(string, ...interface{}) {}
func (safeLogger) Info(string, ...interface{})  {}
func (safeLogger) Warn(string, ...interface{}) {
	log.Print("Temporal worker warning; inspect coordination observations")
}
func (safeLogger) Error(string, ...interface{}) {
	log.Print("Temporal worker error; inspect coordination observations")
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
	profiles, err := coordinationworker.ReadProfiles(c.profilesFile)
	if err != nil {
		return errors.New("execution profiles are invalid or unavailable")
	}
	db, err := store.Open(startup, c.databaseURL)
	if err != nil {
		return errors.New("worker PostgreSQL connection is unavailable")
	}
	defer db.Close()
	if err := db.CheckCoordinationSchema(startup); err != nil {
		return err
	}
	engine, err := client.DialContext(startup, c.temporal.Options("conductor-coordination-worker-v1", safeLogger{}))
	if err != nil {
		return errors.New("Temporal service is unavailable")
	}
	defer engine.Close()
	resolveTarget := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, c.namespace, c.address, coordinationworkflow.TaskQueue)
	}
	target, err := resolveTarget(startup)
	if err != nil {
		return errors.New("Temporal cluster identity, namespace or retention is unavailable or unsupported")
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, c.namespace, c.address, coordinationworkflow.TaskQueue, target)
	if err != nil {
		return err
	}
	runtime, err = runtime.ForWorkflow(coordinationworkflow.WorkflowName)
	if err != nil {
		return err
	}
	activity, err := coordinationworker.NewActivity(db, profiles, os.Getenv("CONDUCTOR_DOCKER_BINARY"))
	if err != nil {
		return err
	}
	dispatcher, err := coordinationworker.NewDispatcher(db, runtime, c.namespace, target, resolveTarget)
	if err != nil {
		return err
	}
	active, stopActive := context.WithCancel(ctx)
	defer stopActive()
	done := make(chan error, 3)
	go func() { done <- coordinationworkflow.RunWorker(active, engine, activity) }()
	go func() {
		done <- dispatcher.Run(active, func(status string) { log.Print("coordination dispatcher: ", status) })
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-active.Done():
				done <- nil
				return
			case <-ticker.C:
				recovery, cancel := context.WithTimeout(active, 25*time.Second)
				err := activity.Recover(recovery)
				cancel()
				if err != nil && active.Err() == nil {
					log.Print("coordination recovery unavailable; reservations remain retained")
				}
			}
		}
	}()
	err = <-done
	stopActive()
	other := <-done
	third := <-done
	if err != nil || other != nil || third != nil {
		return errors.New("coordination worker stopped after a runtime failure")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
