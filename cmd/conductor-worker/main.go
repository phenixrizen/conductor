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

	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/store"
	"go.temporal.io/sdk/client"
)

type config struct{ databaseURL, address, namespace, credentialsFile string }

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE"), credentialsFile: getenv("CONDUCTOR_CONTEXT_CREDENTIALS_FILE")}
	if c.databaseURL == "" || !filepath.IsAbs(c.credentialsFile) || len(c.credentialsFile) > 4096 {
		return c, errors.New("DATABASE_URL and an absolute CONDUCTOR_CONTEXT_CREDENTIALS_FILE are required")
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
	engine, err := client.DialContext(startup, client.Options{
		HostPort: c.address, Namespace: c.namespace, Identity: "conductor-context-worker-v1", Logger: safeLogger{},
		ConnectionOptions: client.ConnectionOptions{MaxPayloadSize: 1 << 20},
	})
	if err != nil {
		return errors.New("local Temporal service is unavailable")
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
