package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/certs"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/reach"
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
	examples := fs.Bool("examples", false, "seed the example crews once (env CONDUCTOR_EXAMPLES=1); a crew whose id exists is left alone")
	yolo := fs.Bool("yolo", false, "launch every agent with its yolo recipe, skipping its permission prompts, unless a launch or a crew says otherwise (env CONDUCTOR_YOLO=1)")
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
	if *examples {
		cfg.Examples = true
	}
	if *yolo {
		cfg.Yolo = true
	}
	if cfg.Yolo {
		log.Warn("yolo is on: agents launch with their yolo recipes and skip their permission prompts (Codex's also drops its sandbox)")
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
	if cfg.Examples {
		added, skipped, err := srv.SeedExampleCrews()
		if err != nil {
			return 1, fmt.Errorf("seed the example crews: %w", err)
		}
		log.Info("example crews", "added", added, "skipped", skipped)
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		// No HTTP/2 on the TLS listener: the WebSocket routes are HTTP/1.1
		// ones, and a browser that negotiated h2 would need the extended
		// CONNECT of RFC 8441 for them. Set before any Serve.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return 1, fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	log.Info("conductor serving", "version", version.String(), "listen", ln.Addr().String(), "publicUrl", cfg.PublicURL, "agents", len(srv.Catalog().List()), "dataDir", cfg.DataDir)
	if cfg.PublicURLIsLocal() {
		log.Info("share links take the address the workbench is opened at; set publicUrl (CONDUCTOR_PUBLIC_URL) for a fixed one")
	}
	if cfg.GeneratedAdminToken {
		// Printed once so a developer can sign in; set CONDUCTOR_ADMIN_TOKEN to avoid this.
		log.Warn("no admin token configured; generated one for this run", "adminToken", cfg.AdminToken)
	}

	mctx, mcancel := context.WithCancel(ctx)
	go srv.RunMaintenance(mctx)

	errCh := make(chan error, 2)
	go func() { errCh <- httpSrv.Serve(ln) }()
	var certMgr *certs.Manager
	var tlsLn net.Listener
	if cfg.TLSEnabled() {
		certMgr, tlsLn, err = startTLS(mctx, cfg, log)
		if err != nil {
			mcancel()
			_ = ln.Close()
			return 1, err
		}
		go func() { errCh <- httpSrv.Serve(tlsLn) }()
	}
	mapper := startReach(mctx, cfg, srv, certMgr, portOf(ln), portOf(tlsLn), log)

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
	if mapper != nil {
		closeCtx, ccancel := context.WithTimeout(context.Background(), 3*time.Second)
		mapper.Close(closeCtx)
		ccancel()
	}
	srv.Shutdown(shutdownCtx)
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	return 0, nil
}

// startTLS makes the certificate manager and the TLS listener: a certificate
// from ACME (for the public address, or the configured domains) or from
// files, served with TLS 1.2 or later, HTTP/1.1 and the acme-tls/1 protocol
// of the tls-alpn-01 challenge. HTTP/2 is not offered: the WebSocket routes
// are HTTP/1.1 ones. Nothing is served before the first issuance.
func startTLS(ctx context.Context, cfg *config.Config, log *slog.Logger) (*certs.Manager, net.Listener, error) {
	o := certs.Options{Dir: filepath.Join(cfg.DataDir, "tls"), CertFile: cfg.TLS.CertFile, KeyFile: cfg.TLS.KeyFile, Log: log}
	if a := cfg.TLS.ACME; a != nil {
		o.Identifiers = a.Domains
		o.Profile = a.Profile
		o.Challenge = a.Challenge
		o.ACME = &certs.ACMEOptions{Email: a.Email, Directory: a.CADirectory, Challenge: a.Challenge, DNSProvider: a.DNSProvider, DNSEnv: a.DNSEnv}
	}
	m, err := certs.New(o)
	if err != nil {
		return nil, nil, err
	}
	raw, err := net.Listen("tcp", cfg.TLS.Listen)
	if err != nil {
		return nil, nil, fmt.Errorf("tls listen %s: %w", cfg.TLS.Listen, err)
	}
	tcfg := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1", "acme-tls/1"}, GetCertificate: m.GetCertificate}
	go m.Run(ctx)
	st := m.Status()
	log.Info("tls listening", "listen", raw.Addr().String(), "mode", st.Mode, "identifiers", st.Identifiers, "challenge", st.Challenge, "ready", st.Ready)
	return m, tls.NewListener(raw, tcfg), nil
}

// startReach starts the reach mapper the config asks for and gives the server
// its status: in auto, the public address by STUN and, when there is a TLS
// listener, its port on the gateway (reachPorts); in manual, the address
// only. Off starts nothing. With a certificate for the public address to
// obtain, the manager learns the address from the mapper once the port is
// mapped (or forwarded by hand). The mapper runs until ctx ends; the caller
// closes it to delete the mappings.
func startReach(ctx context.Context, cfg *config.Config, srv *api.Server, certMgr *certs.Manager, plainPort, tlsPort uint16, log *slog.Logger) *reach.Mapper {
	var cs certSourceOrNil = nil
	if certMgr != nil {
		cs = certMgr
	}
	if cfg.Reach.Mode == config.ReachOff {
		srv.SetReach(nil, cs)
		log.Info("reach: off; share links take the address the workbench is opened at")
		return nil
	}
	ports := reachPorts(cfg, plainPort, tlsPort)
	m := reach.New(reach.Options{
		Mode:       cfg.Reach.Mode,
		Ports:      ports,
		STUNServer: cfg.STUNServer(),
		Verify:     reach.Verifier{Instance: srv.Instance()}.Verify,
		Log:        log,
	})
	srv.SetReach(m, cs)
	if cfg.Reach.Mode == config.ReachAuto && len(ports) == 0 {
		log.Info("reach: auto finds the public address; nothing is mapped without a TLS listener, and share links take the address the workbench is opened at")
	}
	if certMgr != nil && cfg.TLS.ACME != nil && len(cfg.TLS.ACME.Domains) == 0 {
		m.OnChange(func(st reach.Status) {
			if st.ExternalIP.IsValid() && (st.Mapped || st.Method == reach.MethodManual) {
				certMgr.SetIdentifiers([]string{st.ExternalIP.String()})
			} else {
				certMgr.SetIdentifiers(nil)
			}
		})
	}
	go m.Run(ctx)
	return m
}

// certSourceOrNil lets a nil *certs.Manager reach SetReach as a nil interface.
type certSourceOrNil interface {
	Ready() bool
	Status() certs.Status
	HTTP01(token string) (string, bool)
}

// reachPorts are the ports reach maps or reports: the public port to the
// TLS listener, and 80 to the plain listener when reach.publicPort80 asks
// for it (http-01). Plain http is never mapped on its own.
func reachPorts(cfg *config.Config, plainPort, tlsPort uint16) []reach.PortMap {
	if tlsPort == 0 {
		return nil
	}
	ports := []reach.PortMap{{External: uint16(cfg.Reach.PublicPort), Internal: tlsPort}}
	if cfg.Reach.PublicPort80 && plainPort != 0 {
		ports = append(ports, reach.PortMap{External: 80, Internal: plainPort})
	}
	return ports
}

// portOf is the port a listener is bound to (0 for none).
func portOf(ln net.Listener) uint16 {
	if ln == nil {
		return 0
	}
	if ap, err := netip.ParseAddrPort(ln.Addr().String()); err == nil {
		return ap.Port()
	}
	if _, p, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return uint16(n)
		}
	}
	return 0
}

// exitCodeFromEnv is a helper for tests that want to run serve in-process.
var _ = os.Getenv
