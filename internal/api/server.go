// Package api exposes the HTTP and WebSocket surface of `conductor serve`.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/user"
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
	log      *slog.Logger
	web      http.Handler
	store    *store.Store
	// crews holds the saved crews, in crews.json in the data directory; nil
	// when there is no store.
	crews *crew.Store
	// runs launches crews and keeps their runs, in memory.
	runs *crew.Engine
	// runOf answers the run a session is a member of, for run links (see
	// principal.role). It takes no lock of the engine.
	runOf func(sessionID string) (runID string, ok bool)
	// fileDeny lists the directories and files no file read may reach, even
	// inside a session's working directory (see fileDeny).
	fileDeny []string
	// home is the server user's home directory: the integrations routes
	// report and install the agents' hooks there, and nowhere else. Empty
	// when it is unknown.
	home string

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
}

// New wires the server. cat is the configured catalog; when st holds a
// catalog.json overlay it is applied on top. A corrupt or invalid overlay, or
// crews.json, is an error, so that startup fails instead of a later save
// overwriting the file. web serves the embedded SPA and may be nil. st persists
// UI-managed state in the data directory and may be nil when there is none.
func New(cfg *config.Config, cat catalog.Catalog, log *slog.Logger, web http.Handler, st *store.Store) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	base := cat.Clone()
	effective, overlay, err := loadCatalog(st, base)
	if err != nil {
		return nil, err
	}
	var crews *crew.Store
	if st != nil {
		if crews, err = crew.NewStore(st); err != nil {
			return nil, err
		}
	}
	// A home that is not an absolute path (HOME=relative) is no home.
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		home = ""
	}
	s := &Server{
		cfg:      cfg,
		base:     base,
		overlay:  overlay,
		catalog:  effective,
		registry: session.NewRegistry(cfg.MaxSessions),
		links:    share.NewStore(),
		limiter:  newRateLimiter(5, 20),
		log:      log,
		web:      web,
		store:    st,
		crews:    crews,
		fileDeny: fileDeny(cfg, st),
		home:     home,
	}
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
	// of their own.
	s.runs.OnForget = func(runID string) {
		if live := s.links.DeleteRun(runID); len(live) > 0 {
			go s.disconnectLinks(live)
		}
	}
	s.webhooks = startWebhooks(cfg.Webhooks, s.events, s.registry, log)
	// The runs take every entry too: a member's done starts the members after
	// it, and its handoff is typed into the member it names. OnActivity never
	// waits, as a sink must not. The changes of server sessions reach the runs
	// through localChange.
	s.events.addSink(s.runs.OnActivity)
	s.hosts = signal.NewHub(s.registry, log)
	s.hosts.OnChange = s.events.publish
	s.hosts.OnActivity = s.events.activity
	s.registry.OnRemove = func(id string) {
		s.links.DeleteSession(id)
		s.events.removed(id)
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

// Handler returns the routed handler with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/whoami", s.requireAdmin(s.handleWhoAmI))
	mux.HandleFunc("GET /api/catalog", s.requireAdmin(s.handleCatalog))
	mux.HandleFunc("POST /api/catalog", s.requireAdmin(s.handleSaveAgent))
	mux.HandleFunc("POST /api/catalog/check", s.requireAdmin(s.handleCheckCommand))
	mux.HandleFunc("DELETE /api/catalog/{id}", s.requireAdmin(s.handleDeleteAgent))
	mux.HandleFunc("POST /api/catalog/{id}/unhide", s.requireAdmin(s.handleUnhideAgent))
	mux.HandleFunc("GET /api/crews", s.requireAdmin(s.handleListCrews))
	mux.HandleFunc("POST /api/crews", s.requireAdmin(s.handleCreateCrew))
	mux.HandleFunc("PUT /api/crews/{id}", s.requireAdmin(s.handleUpdateCrew))
	mux.HandleFunc("DELETE /api/crews/{id}", s.requireAdmin(s.handleDeleteCrew))
	mux.HandleFunc("POST /api/crews/{id}/duplicate", s.requireAdmin(s.handleDuplicateCrew))
	mux.HandleFunc("POST /api/crews/{id}/launch", s.requireAdmin(s.handleLaunchCrew))
	mux.HandleFunc("GET /api/runs", s.requireAdmin(s.handleListRuns))
	mux.HandleFunc("GET /api/runs/{run}", s.requireAdmin(s.handleGetRun))
	mux.HandleFunc("POST /api/runs/{run}/members", s.requireAdmin(s.handleAddRunMember))
	mux.HandleFunc("POST /api/runs/{run}/members/{name}/start", s.requireAdmin(s.handleStartRunMember))
	mux.HandleFunc("POST /api/runs/{run}/stop", s.requireAdmin(s.handleStopRun))
	mux.HandleFunc("POST /api/runs/{run}/broadcast", s.requireAdmin(s.handleBroadcast))
	mux.HandleFunc("GET /api/runs/{run}/links", s.requireAdmin(s.handleListRunLinks))
	mux.HandleFunc("POST /api/runs/{run}/links", s.requireAdmin(s.handleCreateRunLink))
	mux.HandleFunc("DELETE /api/runs/{run}/links/{linkId}", s.requireAdmin(s.handleRevokeRunLink))
	mux.HandleFunc("GET /api/integrations", s.requireAdmin(s.handleIntegrations))
	mux.HandleFunc("POST /api/integrations/{id}/install", s.requireAdmin(s.handleInstallIntegration))
	mux.HandleFunc("GET /api/sessions", s.requireAdmin(s.handleListSessions))
	mux.HandleFunc("POST /api/sessions", s.requireAdmin(s.handleCreateSession))
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.requireAdmin(s.handleDeleteSession))
	mux.HandleFunc("GET /api/sessions/{id}/links", s.requireAdmin(s.handleListLinks))
	mux.HandleFunc("POST /api/sessions/{id}/links", s.requireAdmin(s.handleCreateLink))
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
	if s.web != nil {
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
		if s.cfg.Dev {
			origin := r.Header.Get("Origin")
			if origin != "" && isDevOrigin(origin) {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				h.Set("Vary", "Origin")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
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
	writeJSON(w, http.StatusOK, map[string]any{"user": serverUser()})
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
		"ok":       true,
		"version":  version.Version,
		"commit":   version.Commit,
		"sessions": s.registry.Count(),
	})
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
// first: the entries the sessions record as they stop are not sent.
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
	s.hosts.CloseAll()
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
