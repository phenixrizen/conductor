package api

import (
	"errors"
	"net/http"

	"github.com/phenixrizen/conductor/internal/agents"
)

// integration is one hook adapter as GET /api/integrations lists it: what
// the agent can report, whether a launch wires its hooks in, whether they are
// installed in the server user's home and where, and the snippet that wires
// them by hand.
type integration struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Events          []string `json:"events"`
	LaunchInjection bool     `json:"launchInjection"`
	Installed       bool     `json:"installed"`
	Where           string   `json:"where"`
	Snippet         string   `json:"snippet"`
	Experimental    bool     `json:"experimental"`
}

// installError is the error body of POST /api/integrations/{id}/install: the
// code and message, the files the install changed before it stopped and,
// when the rest is for the admin to do by hand, the snippet to do it with.
type installError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Snippet string   `json:"snippet,omitempty"`
	Changed []string `json:"changed"`
}

// handleIntegrations lists every adapter, in the order of agents.All, with
// its install checked against the server user's home. Checking writes
// nothing. An adapter with nothing to install reports installed false and
// no place.
func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	hooksDir := agents.HooksDir(s.cfg.DataDir)
	all := agents.All()
	out := make([]integration, 0, len(all))
	for _, a := range all {
		it := integration{
			ID:              a.ID,
			Name:            a.Name,
			Events:          a.Events,
			LaunchInjection: a.Inject != nil,
			Experimental:    a.Experimental,
		}
		if it.Events == nil {
			it.Events = []string{}
		}
		if a.Snippet != nil {
			it.Snippet = a.Snippet(hooksDir)
		}
		if a.Status != nil && s.home != "" {
			it.Installed, it.Where = a.Status(s.home)
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": out})
}

// handleInstallIntegration installs an adapter's hooks into the server user's
// home, and nowhere else, and replies with the files it changed: none when
// they were in place already. An adapter with nothing to install, or an
// install that leaves a step to the admin, answers 400 no_file_route with the
// snippet for it; any other failure answers 500 install_failed. Both report
// the files changed before the install stopped.
func (s *Server) handleInstallIntegration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok := agents.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such integration")
		return
	}
	hooksDir := agents.HooksDir(s.cfg.DataDir)
	snippet := func() string {
		if a.Snippet == nil {
			return ""
		}
		return a.Snippet(hooksDir)
	}
	if a.Install == nil {
		msg := a.Name + " has no file for Conductor to install its hooks into; wire it by hand with the snippet"
		if a.Inject != nil {
			msg = a.Name + " has no file for Conductor to install its hooks into: Conductor wires it at launch; where Conductor does not launch it, wire it with the snippet"
		}
		writeInstallError(w, http.StatusBadRequest, installError{Code: "no_file_route", Message: msg, Snippet: snippet()})
		return
	}
	if s.home == "" {
		writeInstallError(w, http.StatusInternalServerError, installError{Code: "install_failed", Message: "the server user's home directory is unknown"})
		return
	}
	changed, err := a.Install(s.home, hooksDir)
	if changed == nil {
		changed = []string{}
	}
	switch {
	case errors.Is(err, agents.ErrByHand):
		s.log.Info("integration left to install by hand", "integration", id, "changed", len(changed))
		writeInstallError(w, http.StatusBadRequest, installError{Code: "no_file_route", Message: err.Error(), Snippet: snippet(), Changed: changed})
	case err != nil:
		s.log.Error("integration install failed", "integration", id, "changed", len(changed), "err", err)
		writeInstallError(w, http.StatusInternalServerError, installError{Code: "install_failed", Message: err.Error(), Changed: changed})
	default:
		s.log.Info("integration installed", "integration", id, "changed", len(changed))
		writeJSON(w, http.StatusOK, map[string]any{"changed": changed})
	}
}

func writeInstallError(w http.ResponseWriter, status int, e installError) {
	if e.Changed == nil {
		e.Changed = []string{}
	}
	writeJSON(w, status, map[string]installError{"error": e})
}
