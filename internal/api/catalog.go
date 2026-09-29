package api

import (
	"fmt"
	"maps"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/store"
)

// catalogFile is the document in the data directory that holds the overlay the
// Agents page edits. The configured catalog is never written.
const catalogFile = "catalog.json"

// handleCatalog lists the launchable agents, env values masked, and the IDs
// the overlay hides, which POST /api/catalog/{id}/unhide brings back.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	s.catalogMu.Lock()
	cat, hidden := s.catalog, uniqueIDs(s.overlay.Hidden)
	s.catalogMu.Unlock()
	list := cat.List()
	out := make([]catalog.Agent, 0, len(list))
	for _, a := range list {
		out = append(out, a.Redacted())
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out, "hidden": hidden})
}

// handleSaveAgent adds an agent to the overlay, or replaces the agent with the
// same ID (a built-in too), and persists the overlay.
func (s *Server) handleSaveAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	var a catalog.Agent
	if err := decodeJSON(w, r, &a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	// The stored agent is read under the lock the save holds, so no other save
	// can change it in between.
	stored, _ := s.catalog.Get(a.ID)
	if err := restoreMaskedEnv(&a, stored); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_agent", err.Error())
		return
	}
	// Check a by itself, so the message is about this agent and not an index
	// into the overlay.
	var probe catalog.Catalog
	if err := probe.Upsert(a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_agent", err.Error())
		return
	}
	if err := checkAdapter(a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_agent", err.Error())
		return
	}
	ov := s.overlay
	ov.Agents = upsertAgent(ov.Agents, a)
	ov.Hidden = withoutID(ov.Hidden, a.ID)
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", a.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "could not save the catalog")
		return
	}
	s.log.Info("catalog agent saved", "agent", a.ID)
	saved, _ := s.catalog.Get(a.ID)
	writeJSON(w, http.StatusOK, map[string]any{"agent": saved.Redacted()})
}

// handleDeleteAgent removes the overlay entry with the given ID, which restores
// a built-in or configured agent it replaced. An agent that is not in the
// overlay is hidden instead. Sessions already running are not touched.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	id := r.PathValue("id")
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	ov := s.overlay
	inOverlay := slices.ContainsFunc(ov.Agents, func(a catalog.Agent) bool { return a.ID == id })
	_, listed := s.catalog.Get(id)
	action := "removed"
	switch {
	case inOverlay:
		ov.Agents = withoutAgent(ov.Agents, id)
	case listed:
		ov.Hidden = append(slices.Clone(ov.Hidden), id)
		action = "hidden"
	default:
		writeError(w, http.StatusNotFound, "not_found", "no such agent")
		return
	}
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", id, "err", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "could not save the catalog")
		return
	}
	s.log.Info("catalog agent changed", "agent", id, "action", action)
	w.WriteHeader(http.StatusNoContent)
}

// handleUnhideAgent takes an ID off the overlay's hidden list, which brings
// back the built-in or configured agent it hid, and persists the overlay. The
// reply carries that agent, or no agent when none has the ID any more (a
// hand-edited file, or an agent since removed from the config).
func (s *Server) handleUnhideAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	id := r.PathValue("id")
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	ov := s.overlay
	if !slices.Contains(ov.Hidden, id) {
		writeError(w, http.StatusNotFound, "not_found", "no hidden agent with that id")
		return
	}
	ov.Hidden = withoutID(ov.Hidden, id)
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", id, "err", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "could not save the catalog")
		return
	}
	s.log.Info("catalog agent changed", "agent", id, "action", "unhidden")
	out := map[string]any{}
	if a, ok := s.catalog.Get(id); ok {
		out["agent"] = a.Redacted()
	}
	writeJSON(w, http.StatusOK, out)
}

// checkCommandRequest is the body of POST /api/catalog/check.
type checkCommandRequest struct {
	Command []string `json:"command"`
}

// handleCheckCommand reports whether the program a command starts resolves on
// this server, the way exec resolves it when a session launches. Only
// command[0] is looked up and nothing is run. A program that is not found is a
// normal answer, not an error: the agent may be meant for `conductor host`.
func (s *Server) handleCheckCommand(w http.ResponseWriter, r *http.Request) {
	var req checkCommandRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(req.Command) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "command must have at least one element")
		return
	}
	path, err := exec.LookPath(req.Command[0])
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"found": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": true, "path": path})
}

// checkAdapter rejects an agent whose adapter Conductor does not have. The
// catalog cannot check it itself (the adapters import the catalog), so an
// unknown adapter in a config file is only noticed at launch, where it wires
// nothing; the Agents page is told at once.
func checkAdapter(a catalog.Agent) error {
	if a.Adapter == "" {
		return nil
	}
	if _, ok := agents.Get(a.Adapter); ok {
		return nil
	}
	var ids []string
	for _, ad := range agents.All() {
		ids = append(ids, ad.ID)
	}
	return fmt.Errorf("agent %s: unknown adapter %q (one of %s)", a.ID, a.Adapter, strings.Join(ids, ", "))
}

// restoreMaskedEnv puts the stored value back for every env entry of a whose
// value is catalog.RedactedValue. GET /api/catalog and the save response show
// that in place of real values, so a client that edits an agent it read there
// sends it back to mean "unchanged"; saving it as it is would replace the secret
// with the mask. stored is the agent now listed under a.ID, the zero Agent when
// there is none. A masked entry for a key stored does not have has no value to
// keep and is an error; the first such key by name is reported, so the answer
// does not depend on map order.
func restoreMaskedEnv(a *catalog.Agent, stored catalog.Agent) error {
	for _, k := range slices.Sorted(maps.Keys(a.Env)) {
		if a.Env[k] != catalog.RedactedValue {
			continue
		}
		v, ok := stored.Env[k]
		if !ok {
			return fmt.Errorf("env %s: value is redacted; set a real value", k)
		}
		a.Env[k] = v
	}
	return nil
}

// commitOverlay makes ov the current overlay. It builds the catalog ov yields
// from the configured one, writes ov to catalog.json and only then publishes
// both, so a failure leaves the running catalog and the file as they were.
// Deriving the catalog from base and the overlay, as a restart does, keeps the
// two identical. Agents is saved as [] rather than null. The caller holds
// catalogMu.
func (s *Server) commitOverlay(ov catalog.Overlay) error {
	next := s.base.Clone()
	if err := next.ApplyOverlay(ov); err != nil {
		return fmt.Errorf("apply overlay: %w", err)
	}
	if ov.Agents == nil {
		ov.Agents = []catalog.Agent{}
	}
	if err := s.store.Save(catalogFile, ov); err != nil {
		return fmt.Errorf("save %s: %w", catalogFile, err)
	}
	s.overlay, s.catalog = ov, next
	return nil
}

// loadCatalog returns base with the overlay saved in the data directory applied,
// and that overlay. Without a store, or without a catalog.json yet, the overlay
// is empty. Every error is meant to be fatal: an overlay that cannot be read or
// that fails validation must not be replaced by a later save.
func loadCatalog(st *store.Store, base catalog.Catalog) (catalog.Catalog, catalog.Overlay, error) {
	effective := base.Clone()
	if st == nil {
		return effective, catalog.Overlay{}, nil
	}
	path := filepath.Join(st.Dir(), catalogFile)
	var ov catalog.Overlay
	ok, err := st.Load(catalogFile, &ov)
	if err != nil {
		return catalog.Catalog{}, catalog.Overlay{}, fmt.Errorf("%s: %w", path, err)
	}
	if !ok {
		return effective, catalog.Overlay{}, nil
	}
	if err := effective.ApplyOverlay(ov); err != nil {
		return catalog.Catalog{}, catalog.Overlay{}, fmt.Errorf("%s: %w", path, err)
	}
	return effective, ov, nil
}

// upsertAgent returns agents with a in place of the entry with the same ID, or
// appended when there is none. Repeats of the ID, possible in a hand-edited
// file, collapse into a. The input is not modified.
func upsertAgent(agents []catalog.Agent, a catalog.Agent) []catalog.Agent {
	out := make([]catalog.Agent, 0, len(agents)+1)
	replaced := false
	for _, cur := range agents {
		switch {
		case cur.ID != a.ID:
			out = append(out, cur)
		case !replaced:
			out = append(out, a)
			replaced = true
		}
	}
	if !replaced {
		out = append(out, a)
	}
	return out
}

// withoutAgent returns a copy of agents without the entries for id.
func withoutAgent(agents []catalog.Agent, id string) []catalog.Agent {
	return slices.DeleteFunc(slices.Clone(agents), func(a catalog.Agent) bool { return a.ID == id })
}

// uniqueIDs returns a copy of ids without repeats, in order, never nil.
func uniqueIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// withoutID returns a copy of ids without id.
func withoutID(ids []string, id string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(other string) bool { return other == id })
}
