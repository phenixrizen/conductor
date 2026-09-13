// conductor-webhooks accepts authenticated provider reconciliation hints on a
// loopback listener. A trusted HTTPS reverse proxy supplies external transport.
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

	"github.com/phenixrizen/conductor/internal/deliveryworker"
	"github.com/phenixrizen/conductor/internal/store"
)

func run() error {
	address := os.Getenv("CONDUCTOR_WEBHOOK_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8091"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("webhook address must use literal loopback behind an HTTPS reverse proxy")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db, err := store.Open(startup, os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("webhook database unavailable")
	}
	defer db.Close()
	if db.CheckDeliverySchema(startup) != nil {
		return errors.New("publication migrations required")
	}
	handler, err := deliveryworker.LoadWebhookHandler(db, os.Getenv("CONDUCTOR_WEBHOOKS_FILE"))
	if err != nil {
		return errors.New("webhook bindings unavailable or invalid")
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		<-done
		return nil
	case <-done:
		return errors.New("webhook listener stopped")
	}
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
