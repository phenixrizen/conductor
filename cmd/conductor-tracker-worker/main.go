package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/temporalconnection"
	"github.com/phenixrizen/conductor/internal/trackerworker"
	"github.com/phenixrizen/conductor/internal/trackerworkflow"
	"go.temporal.io/sdk/client"
)

type safeLogger struct{}

func (safeLogger) Debug(string, ...interface{}) {}
func (safeLogger) Info(string, ...interface{})  {}
func (safeLogger) Warn(string, ...interface{})  { log.Print("tracker Temporal warning") }
func (safeLogger) Error(string, ...interface{}) { log.Print("tracker Temporal unavailable") }
func loopback(address string) bool {
	host, port, e := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	return e == nil && ip != nil && ip.IsLoopback() && port != ""
}
func run() error {
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL is required")
	}
	temporal, e := temporalconnection.Load(os.Getenv)
	if e != nil {
		return e
	}
	address := temporal.Address
	listen := os.Getenv("CONDUCTOR_TRACKER_WEBHOOK_ADDRESS")
	if listen == "" {
		listen = "127.0.0.1:8091"
	}
	if !loopback(listen) {
		return errors.New("tracker webhook requires a literal loopback address; use a trusted HTTPS reverse proxy")
	}
	credentials, e := trackerworker.LoadCredentials(os.Getenv("CONDUCTOR_TRACKER_CREDENTIALS_FILE"))
	if e != nil {
		return errors.New("tracker credential references unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, e := store.Open(startup, os.Getenv("DATABASE_URL"))
	if e != nil {
		return errors.New("tracker database unavailable")
	}
	defer db.Close()
	engine, e := client.DialContext(startup, temporal.Options("conductor-tracker-worker-v1", safeLogger{}))
	if e != nil {
		return errors.New("tracker Temporal unavailable")
	}
	defer engine.Close()
	activity, e := trackerworker.NewActivity(db, credentials.Resolve, nil)
	if e != nil {
		return e
	}
	dispatcher, e := trackerworker.NewDispatcher(startup, db, engine, address, temporal.Namespace)
	if e != nil {
		return errors.New("tracker runtime binding unavailable")
	}
	listener, e := net.Listen("tcp", listen)
	if e != nil {
		return errors.New("tracker webhook listener unavailable")
	}
	server := &http.Server{Handler: trackerworker.WebhookHandler(db, credentials.Resolve), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second}
	active, cancelActive := context.WithCancel(ctx)
	defer cancelActive()
	done := make(chan error, 3)
	go func() { done <- trackerworkflow.RunWorker(active, engine, activity.Sync) }()
	go func() { done <- dispatcher.Run(active, func(s string) { log.Print(s) }) }()
	go func() { done <- server.Serve(listener) }()
	received := 0
	var failure error
	select {
	case <-ctx.Done():
	case failure = <-done:
		received++
	}
	cancelActive()
	_ = server.Close()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for received < 3 {
		select {
		case e := <-done:
			received++
			if e != nil && !errors.Is(e, http.ErrServerClosed) {
				failure = e
			}
		case <-deadline.C:
			return errors.New("tracker worker shutdown incomplete")
		}
	}
	if failure != nil && !errors.Is(failure, http.ErrServerClosed) {
		return errors.New("tracker worker stopped after a runtime failure")
	}

	return nil
}
func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
