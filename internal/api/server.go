// Package api exposes the HTTP and WebSocket surface of `conductor serve`.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
	"github.com/phenixrizen/conductor/internal/signal"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/version"
)

// Server holds the shared state behind every route.
type Server struct {
	cfg      *config.Config
	registry *session.Registry
	links    *share.Store
	hosts    *signal.Hub
	events   *eventHub
	webhooks *webhookSender
	limiter  *rateLimiter
	// hostLimiter and openHosts bound open hosts on a switchyard, per address:
	// how often one registers and how many live sessions it holds.
	hostLimiter *rateLimiter
	openHosts   *addrCounter
	log         *slog.Logger
	web         http.Handler
	store       *store.Store
	// crews holds the saved crews, one file each in crews/ of the data
	// directory; nil when there is no store.
	crews *crew.Store
	// records keeps the runs that ended, under runs/ in the data directory
	// (crew.Records); nil without one.
	records *crew.Records
	// pastes are the paste-invite peers of each server session (paste.go).
	pastes pasteStore
	// runs launches crews and keeps their runs, in memory.
	runs *crew.Engine
	// runOf answers the run a session is a member of, for run links (see
	// principal.role). It takes no lock of the engine.
	runOf func(sessionID string) (runID string, ok bool)
	// fileDeny lists the directories and files no file read may reach, even
	// inside a session's working directory (see fileDeny).
	fileDeny []string
	// lookups says, for 30 s at a time, whether the programs of the catalog's
	// agents resolve on this server (see lookupCache); probes, for 10 min,
	// what their version output says they are (see probeCache).
	lookups *lookupCache
	probes  *probeCache
	// home is the server user's home directory: the integrations routes
	// report and install the agents' hooks there, and nowhere else, and a
	// launch puts the Conductor skill there (skillDone: the adapters tried
	// since the server started, one attempt each). Empty when it is unknown.
	home      string
	skillMu   sync.Mutex
	skillDone map[string]bool
	// instance identifies this process on /api/health, for the reach
	// self-check; reach and certs are what SetReach gave (see reach.go).
	instance string
	// started is when this process began serving (the switchyard page's uptime);
	// relayed counts the bytes its relay carried, for the operator's figures.
	started time.Time
	relayed *relayMeter
	reachMu sync.Mutex
	reach   reachSource
	certs   certSource
	// publisher publishes sessions to a rendezvous (SetPublisher); published
	// holds the publications by session id, pubGone the ids that left before
	// their publication landed (see publish.go).
	// selfLimits bounds what an agent does with its own session's token
	// (selfservice.go).
	selfLimits *selfCounters
	pubMu      sync.Mutex
	publisher  Publisher
	published  map[string]PublishedSession
	pubGone    map[string]bool

	// catalogEditMu serialises the catalog's editors. An edit holds it from
	// reading overlay to publishing the new catalog, across the write and
	// fsync of catalog.json, so two admins cannot lose each other's change.
	// Readers never take it.
	catalogEditMu sync.Mutex
	// catalogMu guards overlay and catalog for the instant they are read or
	// swapped. catalog is the effective catalog, base with overlay applied;
	// it is replaced as a whole and never edited in place, so a copy taken
	// under the lock (see Catalog) is a stable snapshot. An editor also holds
	// catalogEditMu, so it may read overlay without this lock.
	//
	// overlay's slices (Agents, Hidden) are copy-on-write: an editor reads
	// them under catalogEditMu alone while handleCatalog reads Hidden under
	// catalogMu, so an editor builds new slices (upsertAgent, withoutAgent,
	// withoutID, slices.Clone before append) and publishes them in the swap.
	// Changing one in place, with slices.Delete or an append into its spare
	// capacity, would race with those readers.
	catalogMu sync.Mutex
	base      catalog.Catalog // the configured catalog, before the overlay; never changed after New
	overlay   catalog.Overlay // the UI-managed layer, as saved in catalog.json
	catalog   catalog.Catalog
	// writeCatalog writes an overlay to catalog.json in the store; a test
	// holds it to show that only another edit waits for the write.
	writeCatalog func(catalog.Overlay) error

	mu      sync.Mutex
	counter int

	// bg counts the goroutines the server starts on its own, outside any
	// request: Shutdown waits for them. bgMu guards bgDone, which is set once
	// Shutdown waits; what starts after that is not counted.
	bg     sync.WaitGroup
	bgMu   sync.Mutex
	bgDone bool
}

// New wires the server. cat is the configured catalog; when st holds a
// catalog.json overlay it is applied on top. A corrupt or invalid overlay, or
// crews.json that cannot be moved, is an error, so that startup fails instead
// of a later save overwriting the file; a crew file that cannot be used, and
// a crew of crews.json not moved over an existing file, are logged and left
// out. An override env value equal to the configured agent's is saved back
// as the mask first (maskConfiguredEnv), and a failure to save it stops
// startup too. web serves the embedded SPA and may be nil.
// st persists UI-managed state in the data directory and may be nil when
// there is none.
func New(cfg *config.Config, cat catalog.Catalog, log *slog.Logger, web http.Handler, st *store.Store) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	base := cat.Clone()
	effective, overlay, err := loadCatalog(st, base)
	if err != nil {
		return nil, err
	}
	// An override an earlier version saved holds its env values in full.
	// Those equal to the configured agent's become the mask, as a save stores
	// them now, so a secret rotated in the config later reaches the agent.
	// The values the catalog runs with are the same either way.
	if masked, ids := maskConfiguredEnv(overlay, base); len(ids) > 0 {
		if err := st.Save(catalogFile, masked); err != nil {
			return nil, fmt.Errorf("%s: save with the env values equal to the config's masked: %w", filepath.Join(st.Dir(), catalogFile), err)
		}
		overlay = masked
		log.Info("saved agents now follow the config for env values equal to it", "agents", ids)
	}
	var crews *crew.Store
	if st != nil {
		var problems []error
		if crews, problems, err = crew.NewStore(st); err != nil {
			return nil, err
		}
		// Each problem names its files and says why: a crew file that
		// cannot be used (fix or delete it), or a crew of crews.json that
		// was not moved over a file of the same id.
		for _, p := range problems {
			log.Error("crew left out", "err", p)
		}
	}
	// A home that is not an absolute path (HOME=relative) is no home.
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		home = ""
	}
	s := &Server{
		cfg:         cfg,
		base:        base,
		overlay:     overlay,
		catalog:     effective,
		registry:    session.NewRegistry(cfg.MaxSessions),
		links:       share.NewStore(),
		limiter:     newRateLimiter(5, 20),
		hostLimiter: newRateLimiter(float64(max(1, cfg.Switchyard.OpenHostRegistrationsPerMinute))/60, float64(max(1, cfg.Switchyard.OpenHostRegistrationsPerMinute))),
		openHosts:   &addrCounter{},
		log:         log,
		web:         web,
		store:       st,
		crews:       crews,
		fileDeny:    fileDeny(cfg, st),
		lookups:     newLookupCache(),
		probes:      newProbeCache(),
		home:        home,
		skillDone:   map[string]bool{},
		instance:    newInstance(),
		started:     time.Now(),
		relayed:     newRelayMeter(nil),
	}
	s.selfLimits = newSelfCounters()
	s.writeCatalog = func(ov catalog.Overlay) error { return st.Save(catalogFile, ov) }
	s.events = newEventHub()
	s.runs = crew.NewEngine(s, s.lookupLocal)
	s.runOf = func(sessionID string) (string, bool) {
		runID, _, ok := s.runs.MemberOf(sessionID)
		return runID, ok
	}
	// A run the engine forgets takes its links: under the engine's lock only
	// the share store's maps change (the store never calls the engine); the
	// viewers attached through them are closed as on a revoke, on a goroutine
	// of the server's own (track), which Shutdown waits for.
	s.runs.OnForget = func(runID string) {
		if live := s.links.DeleteRun(runID); len(live) > 0 {
			s.track(func() { s.disconnectLinks(live) })
		}
		s.events.run(runID, true)
	}
	// What a session change does not carry of a run reaches the browsers as
	// a run event, which they read the run again for.
	s.runs.OnRunChange = func(runID string) { s.events.run(runID, false) }
	if st != nil {
		records, err := crew.NewRecords(st)
		if err != nil {
			return nil, err
		}
		s.records = records
		s.runs.RecordEnds(records, func(runID string, err error) { log.Warn("run record not saved", "run", runID, "err", err) }, s.track)
	}
	s.webhooks = startWebhooks(cfg.Webhooks, s.events, s.registry, log)
	// The runs take every entry too: a member's done starts the members after
	// it, and its handoff is typed into the member it names. OnActivity never
	// waits, as a sink must not. The changes of server sessions reach the runs
	// through localChange.
	s.events.addSink(s.runs.OnActivity)
	s.hosts = signal.NewHub(s.registry, log)
	if cfg.MaxViewersPerSession > 0 {
		s.hosts.MaxViewers = cfg.MaxViewersPerSession
	}
	s.hosts.OnChange = s.events.publish
	s.hosts.OnActivity = s.events.activity
	s.registry.OnRemove = func(id string) {
		s.links.DeleteSession(id)
		s.events.removed(id)
		s.unpublish(id)
		s.pastes.closeAll(id)
	}
	s.links.OnRevoke = func(sessionID, linkID string) {
		if d, ok := s.registry.Get(sessionID); ok {
			d.DisconnectLink(linkID)
		}
	}
	s.links.OnRevokeRun = func(runID, linkID string) { s.disconnectLinks([]string{linkID}) }
	return s, nil
}

// disconnectLinks closes the viewers attached through any of linkIDs, run
// links, and returns once they are closed. Their viewers may be on any session
// that was a member of the run, the run forgotten meanwhile included: every
// session is asked, all at once, since each close waits for its viewer's
// reply.
func (s *Server) disconnectLinks(linkIDs []string) {
	var wg sync.WaitGroup
	s.registry.Each(func(d session.Driver) {
		wg.Go(func() {
			for _, id := range linkIDs {
				d.DisconnectLink(id)
			}
		})
	})
	wg.Wait()
}

// track runs f on a goroutine of its own that Shutdown waits for.
func (s *Server) track(f func()) {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.bgDone {
		go f()
		return
	}
	s.bg.Go(f)
}

// WaitBackground waits, as long as ctx allows, for the goroutines the
// server started on its own (track): a run's record being written, the
// viewers of a forgotten run's links being closed. Shutdown does the same
// at its end; tests call it before their directories go.
func (s *Server) WaitBackground(ctx context.Context) {
	s.bgMu.Lock()
	s.bgDone = true
	s.bgMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	// No record lands after this: the directories may be going.
	if s.records != nil {
		s.records.Close()
	}
}

// Handler returns the routed handler with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/reach", s.requireAdmin(s.handleReach))
	mux.HandleFunc("GET /.well-known/acme-challenge/{token}", s.handleACMEChallenge)
	mux.HandleFunc("GET /api/whoami", s.requireAdmin(s.handleWhoAmI))
	mux.HandleFunc("GET /api/switchyard/status", s.requireAdmin(s.handleSwitchyardStatus))
	mux.HandleFunc("GET /api/catalog", s.launching(s.handleCatalog))
	mux.HandleFunc("POST /api/catalog", s.launching(s.handleSaveAgent))
	mux.HandleFunc("POST /api/catalog/check", s.launching(s.handleCheckCommand))
	mux.HandleFunc("DELETE /api/catalog/{id}", s.launching(s.handleDeleteAgent))
	mux.HandleFunc("POST /api/catalog/{id}/unhide", s.launching(s.handleUnhideAgent))
	mux.HandleFunc("GET /api/crews", s.launching(s.handleListCrews))
	mux.HandleFunc("GET /api/crews/{id}", s.launching(s.handleGetCrew))
	mux.HandleFunc("POST /api/crews", s.launching(s.handleCreateCrew))
	mux.HandleFunc("POST /api/crews/examples", s.launching(s.handleSeedExamples))
	mux.HandleFunc("PUT /api/crews/{id}", s.launching(s.handleUpdateCrew))
	mux.HandleFunc("DELETE /api/crews/{id}", s.launching(s.handleDeleteCrew))
	mux.HandleFunc("POST /api/crews/{id}/duplicate", s.launching(s.handleDuplicateCrew))
	mux.HandleFunc("POST /api/crews/{id}/launch", s.launching(s.handleLaunchCrew))
	mux.HandleFunc("GET /api/crews/{id}/runs", s.launching(s.handleCrewRuns))
	mux.HandleFunc("GET /api/paths", s.launching(s.handleListPaths))
	mux.HandleFunc("GET /api/git/check", s.launching(s.handleGitCheck))
	mux.HandleFunc("GET /api/runs", s.launching(s.handleListRuns))
	mux.HandleFunc("GET /api/runs/{run}", s.launching(s.handleGetRun))
	mux.HandleFunc("POST /api/runs/{run}/members", s.launching(s.handleAddRunMember))
	mux.HandleFunc("POST /api/runs/{run}/members/{name}/start", s.launching(s.handleStartRunMember))
	mux.HandleFunc("POST /api/runs/{run}/stop", s.launching(s.handleStopRun))
	mux.HandleFunc("POST /api/runs/{run}/resume", s.launching(s.handleResumeRun))
	mux.HandleFunc("POST /api/runs/{run}/members/{name}/resume", s.launching(s.handleResumeRunMember))
	mux.HandleFunc("POST /api/runs/{run}/broadcast", s.launching(s.handleBroadcast))
	mux.HandleFunc("GET /api/runs/{run}/links", s.launching(s.handleListRunLinks))
	mux.HandleFunc("POST /api/runs/{run}/links", s.launching(s.handleCreateRunLink))
	mux.HandleFunc("DELETE /api/runs/{run}/links/{linkId}", s.launching(s.handleRevokeRunLink))
	mux.HandleFunc("GET /api/integrations", s.launching(s.handleIntegrations))
	mux.HandleFunc("POST /api/integrations/{id}/install", s.launching(s.handleInstallIntegration))
	mux.HandleFunc("GET /api/sessions", s.requireAdmin(s.handleListSessions))
	mux.HandleFunc("POST /api/sessions", s.launching(s.handleCreateSession))
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	// What an agent may do with its own session's token (selfservice.go).
	mux.HandleFunc("POST /api/sessions/{id}/crew", s.launchingAny(s.handleSelfCrew))
	mux.HandleFunc("POST /api/sessions/{id}/run/members", s.launchingAny(s.handleSelfAddMember))
	mux.HandleFunc("GET /api/sessions/{id}/run", s.launchingAny(s.handleSelfRun))
	mux.HandleFunc("POST /api/sessions/{id}/links/agent", s.handleSelfLink)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.requireAdmin(s.handleDeleteSession))
	mux.HandleFunc("POST /api/sessions/{id}/resume", s.launching(s.handleResumeSession))
	mux.HandleFunc("GET /api/sessions/{id}/links", s.requireAdmin(s.handleListLinks))
	mux.HandleFunc("POST /api/sessions/{id}/links", s.requireAdmin(s.handleCreateLink))
	mux.HandleFunc("POST /api/sessions/{id}/paste", s.requireAdmin(s.handlePaste))
	mux.HandleFunc("GET /api/ice", s.handleICE)
	mux.HandleFunc("DELETE /api/sessions/{id}/links/{linkId}", s.requireAdmin(s.handleRevokeLink))
	mux.HandleFunc("GET /api/sessions/{id}/files", s.handleGetFile)
	mux.HandleFunc("POST /api/sessions/{id}/attention", s.handleAttention)
	mux.HandleFunc("POST /api/sessions/{id}/events", s.handleEvent)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/join/{token}", s.handleJoin)
	mux.HandleFunc("GET /ws/sessions/{id}", s.handleViewerWS)
	mux.HandleFunc("GET /ws/host", s.handleHostWS)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})
	if s.cfg.Switchyard.Enabled {
		// A switchyard serves its own pages and the app only for joining.
		mux.Handle("/", s.switchyardFront())
	} else if s.web != nil {
		mux.Handle("/", s.web)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "web UI not built; run make web-build", http.StatusServiceUnavailable)
		})
	}
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if origin := r.Header.Get("Origin"); origin != "" && s.crossOriginAllowed(origin, r.URL.Path) {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		rw := &statusWriter{ResponseWriter: w, status: 200}
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				if !rw.wrote {
					writeError(w, http.StatusInternalServerError, "internal", "internal error")
				}
			}
			// Log the path only: query strings may carry share tokens.
			s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(rw, r)
	})
}

// launching wraps an admin route that launches or edits what is launched
// (sessions, crews, runs, the catalog, the integrations): a switchyard has
// none of it and answers 403 switchyard.
func (s *Server) launching(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAdmin(s.launchingAny(next))
}

// launchingAny is launching for a route with its own authentication.
func (s *Server) launchingAny(next http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.Switchyard.Enabled {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "switchyard", "this server coordinates hosted sessions and launches none")
	}
}

// crossOriginAllowed reports whether a browser page at origin may call path
// from another origin: any localhost origin and any path in dev mode (the
// Nuxt dev server), and, on a switchyard, its allowed origins (loopback by
// default: the desktop app's own workbench) for the join route alone. The
// WebSocket routes check origins on their own (acceptOptions).
func (s *Server) crossOriginAllowed(origin, path string) bool {
	if s.cfg.Dev && isDevOrigin(origin) {
		return true
	}
	if s.cfg.Switchyard.Enabled && strings.HasPrefix(path, "/api/join/") {
		return originMatches(origin, s.cfg.SwitchyardOrigins())
	}
	return false
}

// originMatches reports whether origin's host matches one of the patterns,
// in the form the WebSocket accept takes (host[:port], with * wildcards).
func originMatches(origin string, patterns []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, p := range patterns {
		if ok, err := path.Match(p, u.Host); err == nil && ok {
			return true
		}
	}
	return false
}

func isDevOrigin(origin string) bool {
	for _, p := range []string{"http://localhost:", "http://127.0.0.1:"} {
		if strings.HasPrefix(origin, p) {
			return true
		}
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the hijacker for WebSockets.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// handleWhoAmI tells an admin which OS user runs the server: the workbench
// uses it as the default display name. It is a label, not authentication.
func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{"user": serverUser(), "host": host, "switchyard": s.cfg.Switchyard.Enabled})
}

// serverUser is the OS user running this process, bounded to 64 bytes.
func serverUser() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if name == "" {
		name = os.Getenv("USER")
	}
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"version":    version.Version,
		"commit":     version.Commit,
		"sessions":   s.registry.Count(),
		"instance":   s.instance,
		"switchyard": s.cfg.Switchyard.Enabled,
		"relay":      !s.cfg.Switchyard.Enabled || s.cfg.SwitchyardRelay(),
	})
}

// newInstance is a random id for this process (16 hex digits).
func newInstance() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// RunMaintenance garbage-collects ended sessions until ctx is cancelled.
func (s *Server) RunMaintenance(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, id := range s.registry.GC(time.Duration(s.cfg.ExitedRetention), now.UTC()) {
				s.log.Info("session garbage collected", "session", id)
			}
			s.hosts.Expire(now)
		}
	}
}

// Shutdown stops the webhooks, then the runs, so that no crew member starts
// any more, then every session, and closes every viewer. The webhooks go
// first: the entries the sessions record as they stop are not sent. The
// goroutines the server started on its own (track) are waited for last, once
// every viewer is closed, until they end or ctx does.
func (s *Server) Shutdown(ctx context.Context) {
	s.webhooks.close(ctx)
	for _, run := range s.runs.List() {
		if run.StoppedAt == nil {
			_ = s.runs.Stop(ctx, run.ID)
		}
	}
	s.registry.Each(func(d session.Driver) {
		if local, ok := d.(*session.Local); ok {
			_ = local.Stop(ctx)
			local.CloseAll(errShuttingDown)
		}
	})
	// The publications end with their sessions; wait for them to tell the
	// rendezvous, all at once, as long as ctx allows.
	s.pubMu.Lock()
	pubs := make([]PublishedSession, 0, len(s.published))
	for _, p := range s.published {
		pubs = append(pubs, p)
	}
	s.published = nil
	s.pubMu.Unlock()
	if len(pubs) > 0 {
		var wg sync.WaitGroup
		for _, p := range pubs {
			wg.Go(p.Stop)
		}
		stopped := make(chan struct{})
		go func() { wg.Wait(); close(stopped) }()
		select {
		case <-stopped:
		case <-ctx.Done():
		}
	}
	s.hosts.CloseAll()
	// What the server started on its own (the viewers of a forgotten run's
	// links being closed) ends before Shutdown returns, or ctx does.
	s.bgMu.Lock()
	s.bgDone = true
	s.bgMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	if s.records != nil {
		s.records.Close()
	}
}

// Registry exposes the session index (used by tests and the CLI).
func (s *Server) Registry() *session.Registry { return s.registry }

// Links exposes the share store.
func (s *Server) Links() *share.Store { return s.links }

// Catalog returns a snapshot of the launchable agents: the configured catalog
// with the data directory overlay applied. A snapshot is never changed by later
// edits, so it can be read without a lock.
func (s *Server) Catalog() catalog.Catalog {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	return s.catalog
}
