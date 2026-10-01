package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/share"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/version"
	"github.com/phenixrizen/conductor/internal/web"
)

// writeAssets is agents.WriteAssets: a test replaces it to make a mode fail.
var writeAssets = agents.WriteAssets

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to a JSON config file")
	listen := fs.String("listen", "", "listen address (overrides config)")
	dev := fs.Bool("dev", false, "allow the Nuxt dev server origin and CORS from localhost")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn, error")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return 2, fmt.Errorf("invalid log level %q", *logLevel)
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		return 1, err
	}
	for _, w := range cfg.Warnings() {
		log.Warn(w)
	}
	notice, err := cfg.ResolveDataDir(*configPath)
	if err != nil {
		return 1, err
	}
	if notice != "" {
		log.Warn(notice)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *dev {
		cfg.Dev = true
	}
	if cfg.AdminToken == "" {
		tok, _ := share.NewToken()
		cfg.AdminToken = tok
		cfg.GeneratedAdminToken = true
	}
	cat, err := cfg.LoadCatalog()
	if err != nil {
		return 1, err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return 1, fmt.Errorf("data directory %s is not usable (%w); set dataDir in the config or CONDUCTOR_DATA_DIR to a writable directory", cfg.DataDir, err)
	}
	// Launches inject flags that name the hook assets in the data directory,
	// and the assets run this binary: the server does not start without them.
	exe, err := agents.BinaryPath()
	if err != nil {
		return 1, fmt.Errorf("locate the conductor binary for the hook assets: %w", err)
	}
	if err := writeAssets(agents.HooksDir(cfg.DataDir), exe); err != nil {
		var me *agents.ModeError
		if !errors.As(err, &me) {
			return 1, fmt.Errorf("write the hook assets to %s: %w", agents.HooksDir(cfg.DataDir), err)
		}
		log.Warn("serving with hook assets whose modes could not be set", "dir", agents.HooksDir(cfg.DataDir), "err", err)
	}
	if root := cfg.DataDirOverlap(); root != "" {
		log.Warn("the data directory overlaps an allowed root: agents working there can read it and commit its secrets, and the file viewer refuses it; keep it outside allowedRoots (the default is ~/.conductor), or set dataDir or CONDUCTOR_DATA_DIR to a directory outside them", "dataDir", cfg.DataDir, "allowedRoot", root)
	}

	ui := web.Handler()
	if ui == nil {
		log.Warn("web UI is not embedded; run `make web-build` before building, API only")
	}
	srv, err := api.New(cfg, cat, log, ui, st)
	if err != nil {
		return 1, err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return 1, fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	log.Info("conductor serving", "version", version.String(), "listen", ln.Addr().String(), "publicUrl", cfg.PublicURL, "agents", len(srv.Catalog().List()), "dataDir", cfg.DataDir)
	if cfg.GeneratedAdminToken {
		// Printed once so a developer can sign in; set CONDUCTOR_ADMIN_TOKEN to avoid this.
		log.Warn("no admin token configured; generated one for this run", "adminToken", cfg.AdminToken)
	}

	mctx, mcancel := context.WithCancel(ctx)
	go srv.RunMaintenance(mctx)

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		mcancel()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return 1, err
		}
		return 0, nil
	}
	mcancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	return 0, nil
}

// exitCodeFromEnv is a helper for tests that want to run serve in-process.
var _ = os.Getenv
