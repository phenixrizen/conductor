package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/coordinationworker"
	"github.com/phenixrizen/conductor/internal/coordinationworkflow"
	"github.com/phenixrizen/conductor/internal/store"
	"go.temporal.io/sdk/client"
)

type config struct{ databaseURL, address, namespace, profilesFile string }

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE"), profilesFile: getenv("CONDUCTOR_EXECUTION_PROFILES_FILE")}
	if c.databaseURL == "" || !filepath.IsAbs(c.profilesFile) || len(c.profilesFile) > 4096 {
		return c, errors.New("DATABASE_URL and an absolute CONDUCTOR_EXECUTION_PROFILES_FILE are required")
	}
	// The first worker supports an explicitly trusted local Temporal service.
	// Remote TLS/mTLS and hosted Temporal authentication require another profile.
	if getenv("CONDUCTOR_TEMPORAL_MODE") != "local" {
		return c, errors.New("CONDUCTOR_TEMPORAL_MODE must explicitly be local")
	}
	if c.address == "" {
		c.address = "127.0.0.1:7233"
	}
	host, port, err := net.SplitHostPort(c.address)
	portNumber, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || ip == nil || !ip.IsLoopback() {
		return c, errors.New("CONDUCTOR_TEMPORAL_ADDRESS must be a literal loopback address and port")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`).MatchString(c.namespace) {
		return c, errors.New("CONDUCTOR_TEMPORAL_NAMESPACE is required and must be a bounded namespace name")
	}
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
	engine, err := client.DialContext(startup, client.Options{
		HostPort: c.address, Namespace: c.namespace, Identity: "conductor-coordination-worker-v1", Logger: safeLogger{},
		ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20},
	})
	if err != nil {
		return errors.New("local Temporal service is unavailable")
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
