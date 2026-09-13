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

type config struct {
	databaseURL, address, mode, issuer, audience string
	publicOrigin, clientID, clientSecretFile     string
	contextCollections                           bool
	coordination                                 bool
	deliveries                                   bool
	runtimeEvidence                              bool
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{databaseURL: getenv("DATABASE_URL"), address: getenv("CONDUCTOR_ADDR"),
		mode: getenv("CONDUCTOR_AUTH_MODE"), issuer: getenv("CONDUCTOR_OIDC_ISSUER"), audience: getenv("CONDUCTOR_OIDC_AUDIENCE"),
		publicOrigin: getenv("CONDUCTOR_PUBLIC_ORIGIN"), clientID: getenv("CONDUCTOR_OIDC_CLIENT_ID"), clientSecretFile: getenv("CONDUCTOR_OIDC_CLIENT_SECRET_FILE")}
	if c.databaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	switch getenv("CONDUCTOR_CONTEXT_COLLECTIONS") {
	case "", "0":
	case "1":
		c.contextCollections = true
	default:
		return c, errors.New("CONDUCTOR_CONTEXT_COLLECTIONS must be unset, 0, or 1")
	}
	switch getenv("CONDUCTOR_COORDINATION") {
	case "", "0":
	case "1":
		c.coordination = true
	default:
		return c, errors.New("CONDUCTOR_COORDINATION must be unset, 0, or 1")
	}
	switch getenv("CONDUCTOR_DELIVERIES") {
	case "", "0":
	case "1":
		c.deliveries = true
	default:
		return c, errors.New("CONDUCTOR_DELIVERIES must be unset, 0, or 1")
	}
	switch getenv("CONDUCTOR_RUNTIME_EVIDENCE") {
	case "", "0":
	case "1":
		c.runtimeEvidence = true
	default:
		return c, errors.New("CONDUCTOR_RUNTIME_EVIDENCE must be unset, 0, or 1")
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
		if c.runtimeEvidence {
			return c, errors.New("runtime evidence requires OIDC authentication")
		}
		if c.deliveries {
			return c, errors.New("repository publication requires OIDC authentication")
		}
		if c.coordination {
			return c, errors.New("coordinated execution requires OIDC authentication")
		}
		if c.contextCollections {
			return c, errors.New("remote context collection requires OIDC authentication; local mode cannot enable it")
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return c, errors.New("local authentication requires a literal loopback listen address")
		}
		if c.issuer != "" || c.audience != "" || c.publicOrigin != "" || c.clientID != "" || c.clientSecretFile != "" {
			return c, errors.New("OIDC settings cannot be combined with local authentication")
		}
	case "oidc":
		if c.issuer == "" || c.audience == "" {
			return c, errors.New("OIDC mode requires CONDUCTOR_OIDC_ISSUER and CONDUCTOR_OIDC_AUDIENCE")
		}
		if c.publicOrigin != "" || c.clientID != "" || c.clientSecretFile != "" {
			if c.publicOrigin == "" || c.clientID == "" || c.clientSecretFile == "" {
				return c, errors.New("browser sign-in requires CONDUCTOR_PUBLIC_ORIGIN, CONDUCTOR_OIDC_CLIENT_ID, and CONDUCTOR_OIDC_CLIENT_SECRET_FILE together")
			}
			var err error
			c.publicOrigin, err = api.BrowserOrigin(c.publicOrigin)
			if err != nil {
				return c, err
			}
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
	var browser *authn.Browser
	if c.mode == "oidc" {
		var secret string
		if c.clientSecretFile != "" {
			secret, err = browserClientSecret(c.clientSecretFile)
			if err != nil {
				return err
			}
		}
		verifier, err = authn.New(startup, authn.Config{Issuer: c.issuer, Audience: c.audience})
		if err != nil {
			return fmt.Errorf("initialize access-token verification: %w", err)
		}
		if c.publicOrigin != "" {
			browser, err = authn.NewBrowser(startup, authn.BrowserConfig{Issuer: c.issuer, ClientID: c.clientID, ClientSecret: secret, RedirectURL: c.publicOrigin + "/api/v1/auth/callback"})
			if err != nil {
				return errors.New("initialize browser sign-in: identity configuration is unavailable or unsupported")
			}
		}
	}
	db, err := store.Open(startup, c.databaseURL)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer db.Close()
	if c.runtimeEvidence {
		if err := db.CheckRuntimeSchema(startup); err != nil {
			return errors.New("runtime evidence migrations are required")
		}
	}
	if c.contextCollections {
		if err := db.CheckContextSchema(startup); err != nil {
			return err
		}
	}
	var handler http.Handler
	if c.mode == "oidc" {
		shared := service.NewAuthenticated(db)
		if c.runtimeEvidence {
			shared = shared.WithRuntimeEvidence()
		}
		if c.deliveries {
			shared = shared.WithDeliveries()
		}
		if c.coordination {
			shared = shared.WithCoordination()
		}
		if c.contextCollections {
			// This opens the API capability only. Each repository still requires
			// operator enablement and transactional author/read permission checks.
			shared = shared.WithCollections()
		}
		if browser != nil {
			handler, err = api.NewBrowserAuthenticated(shared, verifier, browser, db, api.BrowserConfig{Origin: c.publicOrigin, Issuer: c.issuer})
			if err != nil {
				return err
			}
		} else {
			handler = api.NewAuthenticated(shared, verifier)
		}
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
