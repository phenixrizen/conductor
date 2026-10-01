package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/store"
)

// catalogFile is the document in the data directory that holds the overlay the
// Agents page edits. The configured catalog is never written.
const catalogFile = "catalog.json"

// catalogEntry is an agent as the catalog routes answer with it: env values
// masked, where the catalog took it from and, for a saved agent that replaces
// a built-in or configured one, where that one came from (deleting the saved
// agent brings it back).
type catalogEntry struct {
	catalog.Agent
	Source   catalog.Source `json:"source,omitempty"`
	Replaces catalog.Source `json:"replaces,omitempty"`
}

// entry is a as the routes show it, by the effective catalog cat and the
// configured one, base.
func entry(a catalog.Agent, cat, base catalog.Catalog) catalogEntry {
	e := catalogEntry{Agent: a.Redacted(), Source: cat.Source(a.ID)}
	if e.Source == catalog.SourceSaved {
		e.Replaces = base.Source(a.ID)
	}
	return e
}

// saveAgentRequest is the body of POST /api/catalog: an agent, as a client
// builds it or as GET /api/catalog lists it. source and replaces, which the
// listing adds, are taken and ignored, so an agent read there can be sent back
// as it is; any other field the agent does not have is refused.
type saveAgentRequest struct {
	catalog.Agent
	Source   json.RawMessage `json:"source,omitempty"`
	Replaces json.RawMessage `json:"replaces,omitempty"`
}

// handleCatalog lists the launchable agents, env values masked, and the IDs
// the overlay hides, which POST /api/catalog/{id}/unhide brings back.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	s.catalogMu.Lock()
	cat, hidden := s.catalog, uniqueIDs(s.overlay.Hidden)
	s.catalogMu.Unlock()
	list := cat.List()
	out := make([]catalogEntry, 0, len(list))
	for _, a := range list {
		out = append(out, entry(a, cat, s.base))
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
	var req saveAgentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	saved, aerr := s.saveAgent(req.Agent)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": saved})
}

// saveAgent adds a to the overlay, or replaces the entry with its ID, and
// returns it as the catalog now lists it. It holds catalogEditMu from reading
// the stored agent to building the reply, so the reply is this save's and no
// later edit's; readers and launches wait for none of it.
func (s *Server) saveAgent(a catalog.Agent) (catalogEntry, *apiError) {
	s.catalogEditMu.Lock()
	defer s.catalogEditMu.Unlock()
	// The overlay is read under the editor lock, so no other save can change
	// it in between.
	base, _ := s.base.Get(a.ID)
	if err := keepMaskedEnv(&a, savedAgent(s.overlay.Agents, a.ID), base); err != nil {
		return catalogEntry{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
	// Check a by itself, so the message is about this agent and not an index
	// into the overlay.
	var probe catalog.Catalog
	if err := probe.Upsert(a); err != nil {
		return catalogEntry{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
	if err := checkAdapter(a); err != nil {
		return catalogEntry{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
	ov := s.overlay
	ov.Agents = upsertAgent(ov.Agents, a)
	ov.Hidden = withoutID(ov.Hidden, a.ID)
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", a.ID, "err", err)
		return catalogEntry{}, newAPIError(http.StatusInternalServerError, "store_failed", "could not save the catalog")
	}
	s.log.Info("catalog agent saved", "agent", a.ID)
	cat := s.Catalog()
	saved, _ := cat.Get(a.ID)
	return entry(saved, cat, s.base), nil
}

// handleDeleteAgent removes the overlay entry with the given ID, which restores
// a built-in or configured agent it replaced. An agent that is not in the
// overlay is hidden instead. Sessions already running are not touched.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	if aerr := s.deleteAgent(r.PathValue("id")); aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAgent removes the overlay entry with the given ID, or hides an agent
// that has none, under catalogEditMu.
func (s *Server) deleteAgent(id string) *apiError {
	s.catalogEditMu.Lock()
	defer s.catalogEditMu.Unlock()
	ov := s.overlay
	inOverlay := slices.ContainsFunc(ov.Agents, func(a catalog.Agent) bool { return a.ID == id })
	_, listed := s.Catalog().Get(id)
	action := "removed"
	switch {
	case inOverlay:
		ov.Agents = withoutAgent(ov.Agents, id)
	case listed:
		ov.Hidden = append(slices.Clone(ov.Hidden), id)
		action = "hidden"
	default:
		return newAPIError(http.StatusNotFound, "not_found", "no such agent")
	}
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", id, "err", err)
		return newAPIError(http.StatusInternalServerError, "store_failed", "could not save the catalog")
	}
	s.log.Info("catalog agent changed", "agent", id, "action", action)
	return nil
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
	a, found, aerr := s.unhideAgent(r.PathValue("id"))
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	out := map[string]any{}
	if found {
		out["agent"] = a
	}
	writeJSON(w, http.StatusOK, out)
}

// unhideAgent takes id off the hidden list under catalogEditMu and returns the
// agent it brings back, if one has that ID, as the reply shows it, built
// before the lock is released.
func (s *Server) unhideAgent(id string) (catalogEntry, bool, *apiError) {
	s.catalogEditMu.Lock()
	defer s.catalogEditMu.Unlock()
	ov := s.overlay
	if !slices.Contains(ov.Hidden, id) {
		return catalogEntry{}, false, newAPIError(http.StatusNotFound, "not_found", "no hidden agent with that id")
	}
	ov.Hidden = withoutID(ov.Hidden, id)
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", id, "err", err)
		return catalogEntry{}, false, newAPIError(http.StatusInternalServerError, "store_failed", "could not save the catalog")
	}
	s.log.Info("catalog agent changed", "agent", id, "action", "unhidden")
	cat := s.Catalog()
	a, ok := cat.Get(id)
	return entry(a, cat, s.base), ok, nil
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

// checkAdapter rejects an agent whose adapter Conductor does not have.
func checkAdapter(a catalog.Agent) error {
	if err := agents.CheckAdapter(a.Adapter); err != nil {
		return fmt.Errorf("agent %s: %w", a.ID, err)
	}
	return nil
}

// keepMaskedEnv decides what the overlay stores for each env entry of a. A
// value of catalog.RedactedValue is what GET /api/catalog shows in place of
// every value, so a client that sends back what it read means "unchanged". If
// the overlay entry a replaces (saved) holds a value of its own for the key,
// that value is kept. Otherwise, if the agent a overrides (base) has the key,
// the mask itself is stored: ApplyOverlay reads it as base's value, so a
// change in the config reaches the agent. A masked key that neither has is an
// error. A value, sent or kept, equal to base's for the key is stored as the
// mask too, as ApplyOverlay reads it (New applies the same rule to what an
// earlier version saved). The first error by key name is reported, so the
// answer does not depend on map order.
func keepMaskedEnv(a *catalog.Agent, saved, base catalog.Agent) error {
	for _, k := range slices.Sorted(maps.Keys(a.Env)) {
		v := a.Env[k]
		bv, inBase := base.Env[k]
		if v == catalog.RedactedValue {
			sv, ok := saved.Env[k]
			switch {
			case ok && sv != catalog.RedactedValue:
				v = sv
			case !inBase:
				return fmt.Errorf("env %s: value is redacted; set a real value", k)
			}
		}
		if inBase && v == bv {
			v = catalog.RedactedValue // the base agent's value
		}
		a.Env[k] = v
	}
	return nil
}

// maskConfiguredEnv returns ov with every env value of an override equal to
// the value the configured agent it replaces (in base) has for that key set
// to catalog.RedactedValue, the rule keepMaskedEnv applies on save, and the
// IDs of the overrides it changed, each once (none when nothing changed, and
// then ov itself). ov is not modified.
func maskConfiguredEnv(ov catalog.Overlay, base catalog.Catalog) (catalog.Overlay, []string) {
	var ids []string
	var out []catalog.Agent
	for i, a := range ov.Agents {
		b, ok := base.Get(a.ID)
		if !ok {
			continue
		}
		var env map[string]string
		for k, v := range a.Env {
			if bv, inBase := b.Env[k]; inBase && v == bv && v != catalog.RedactedValue {
				if env == nil {
					env = maps.Clone(a.Env)
				}
				env[k] = catalog.RedactedValue
			}
		}
		if env == nil {
			continue
		}
		if out == nil {
			out = slices.Clone(ov.Agents)
		}
		out[i].Env = env
		if !slices.Contains(ids, a.ID) {
			ids = append(ids, a.ID)
		}
	}
	if ids == nil {
		return ov, nil
	}
	ov.Agents = out
	return ov, ids
}

// savedAgent returns the overlay's entry for id, the last one when a
// hand-edited file repeats it (the one that wins at startup), or the zero
// Agent.
func savedAgent(agents []catalog.Agent, id string) catalog.Agent {
	for i := len(agents) - 1; i >= 0; i-- {
		if agents[i].ID == id {
			return agents[i]
		}
	}
	return catalog.Agent{}
}

// commitOverlay makes ov the current overlay. It builds the catalog ov yields
// from the configured one and writes ov to catalog.json without catalogMu.
// Only then does it publish both, under catalogMu for that instant, so a
// failure leaves the running catalog and the file as they were. Deriving the
// catalog from base and the overlay, as a restart does, keeps the two
// identical. Agents is saved as [] rather than null. The caller holds
// catalogEditMu.
func (s *Server) commitOverlay(ov catalog.Overlay) error {
	next := s.base.Clone()
	if err := next.ApplyOverlay(ov); err != nil {
		return fmt.Errorf("apply overlay: %w", err)
	}
	if ov.Agents == nil {
		ov.Agents = []catalog.Agent{}
	}
	if err := s.writeCatalog(ov); err != nil {
		return fmt.Errorf("save %s: %w", catalogFile, err)
	}
	s.catalogMu.Lock()
	s.overlay, s.catalog = ov, next
	s.catalogMu.Unlock()
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
	for i, a := range ov.Agents {
		if err := checkAdapter(a); err != nil {
			return catalog.Catalog{}, catalog.Overlay{}, fmt.Errorf("%s: agents[%d]: %w", path, i, err)
		}
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
