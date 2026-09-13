package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
)

type config struct{ databaseURL, address, mode, issuer, audience string }

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_ADDR"),
		mode: getenv("CONDUCTOR_AUTH_MODE"), issuer: getenv("CONDUCTOR_OIDC_ISSUER"), audience: getenv("CONDUCTOR_OIDC_AUDIENCE")}
	if c.databaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.address == "" {
		c.address = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(c.address)
	if err != nil {
		return c, errors.New("CONDUCTOR_ADDR must include a host and port")
	}
	switch c.mode {
	case "local":
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return c, errors.New("local authentication requires a literal loopback listen address")
		}
		if c.issuer != "" || c.audience != "" {
			return c, errors.New("OIDC settings cannot be combined with local authentication")
		}
	case "oidc":
		if c.issuer == "" || c.audience == "" {
			return c, errors.New("OIDC mode requires CONDUCTOR_OIDC_ISSUER and CONDUCTOR_OIDC_AUDIENCE")
		}
	default:
		return c, errors.New("CONDUCTOR_AUTH_MODE must explicitly be local or oidc")
	}
	return c, nil
}

func run() error {
	c, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var verifier *authn.Verifier
	if c.mode == "oidc" {
		verifier, err = authn.New(startup, authn.Config{Issuer: c.issuer, Audience: c.audience})
		if err != nil {
			return fmt.Errorf("initialize access-token verification: %w", err)
		}
	}
	db, err := store.Open(startup, c.databaseURL)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer db.Close()
	var handler http.Handler
	if c.mode == "oidc" {
		handler = api.NewAuthenticated(service.NewAuthenticated(db), verifier)
	} else {
		handler = api.New(service.New(db))
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	listener, err := net.Listen("tcp", c.address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer server.Close()
	log.Printf("conductord listening on %s (authentication mode: %s)", listener.Addr(), c.mode)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// Let already authorized commands finish their transactions before closing
		// the database. Uncertain client outcomes still require explicit inspection.
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
