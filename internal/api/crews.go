package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/phenixrizen/conductor/internal/crew"
)

// maxCrewBody bounds the body of a crew create or update. A crew is at most
// 512 KiB as the server writes it (crew.Validate); a client may escape more
// of it than the server does.
const maxCrewBody = 1 << 20

// crewInput is the body of POST /api/crews and PUT /api/crews/{id}: a crew
// without what the server sets. A client that sends id, createdAt or updatedAt
// is refused like one that sends any other unknown field.
type crewInput struct {
	Name               string        `json:"name"`
	Goal               string        `json:"goal"`
	Cwd                string        `json:"cwd"`
	Where              string        `json:"where"`
	Isolation          string        `json:"isolation"`
	OpenAfterLaunch    bool          `json:"openAfterLaunch"`
	ViewLinkTTLSeconds int64         `json:"viewLinkTtlSeconds"`
	Members            []crew.Member `json:"members"`
}

// handleListCrews lists the saved crews by name. Without a data directory
// there are none.
func (s *Server) handleListCrews(w http.ResponseWriter, r *http.Request) {
	list := []crew.Crew{}
	if s.crews != nil {
		list = s.crews.List()
	}
	writeJSON(w, http.StatusOK, map[string]any{"crews": list})
}

// handleCreateCrew saves a new crew under an ID the store derives from its name.
func (s *Server) handleCreateCrew(w http.ResponseWriter, r *http.Request) {
	c, ok := s.readCrew(w, r)
	if !ok {
		return
	}
	saved, err := s.crews.Create(c)
	if err != nil {
		s.crewStoreError(w, "", err)
		return
	}
	s.log.Info("crew created", "crew", saved.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"crew": saved})
}

// handleUpdateCrew replaces a crew's fields with the body. Its ID and creation
// time never change.
func (s *Server) handleUpdateCrew(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, ok := s.readCrew(w, r)
	if !ok {
		return
	}
	saved, err := s.crews.Update(id, c)
	if err != nil {
		s.crewStoreError(w, id, err)
		return
	}
	s.log.Info("crew updated", "crew", id)
	writeJSON(w, http.StatusOK, map[string]any{"crew": saved})
}

// handleDeleteCrew deletes a crew.
func (s *Server) handleDeleteCrew(w http.ResponseWriter, r *http.Request) {
	if s.crews == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	id := r.PathValue("id")
	found, err := s.crews.Delete(id)
	if err != nil {
		s.crewStoreError(w, id, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "no such crew")
		return
	}
	s.log.Info("crew deleted", "crew", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleDuplicateCrew saves a copy of a crew under <id>-copy. A crew with an
// agent the catalog no longer has is not copied.
func (s *Server) handleDuplicateCrew(w http.ResponseWriter, r *http.Request) {
	if s.crews == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	id := r.PathValue("id")
	src, ok := s.crews.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such crew")
		return
	}
	if err := src.CheckAgents(s.Catalog()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return
	}
	saved, err := s.crews.Duplicate(id)
	if err != nil {
		s.crewStoreError(w, id, err)
		return
	}
	s.log.Info("crew duplicated", "crew", id, "copy", saved.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"crew": saved})
}

// readCrew decodes the body of a create or an update into a crew and checks
// it: first its rules, on the name trimmed as the store saves it, then its
// agents against the catalog. So a crew that breaks a rule is refused for it
// (400) before the store looks at the crew limit (409) or at the id of an
// update (404), and an unknown agent is 400 whatever else holds. The store
// checks the crew again as it saves it, with its id and times. When readCrew
// reports false it has answered the request.
func (s *Server) readCrew(w http.ResponseWriter, r *http.Request) (crew.Crew, bool) {
	if s.crews == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return crew.Crew{}, false
	}
	var in crewInput
	if err := decodeJSONLimit(w, r, &in, maxCrewBody); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return crew.Crew{}, false
	}
	c := crew.Crew{
		Name: in.Name, Goal: in.Goal, Cwd: in.Cwd, Where: in.Where, Isolation: in.Isolation,
		OpenAfterLaunch: in.OpenAfterLaunch, ViewLinkTTLSeconds: in.ViewLinkTTLSeconds, Members: in.Members,
	}
	trimmed := c
	trimmed.Name = strings.TrimSpace(c.Name)
	if err := trimmed.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return crew.Crew{}, false
	}
	if err := c.CheckAgents(s.Catalog()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
		return crew.Crew{}, false
	}
	return c, true
}

// crewStoreError answers an error from the crew store. A failed save is
// logged with its cause, which names the data directory; the reply does not.
func (s *Server) crewStoreError(w http.ResponseWriter, id string, err error) {
	switch {
	case errors.Is(err, crew.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
	case errors.Is(err, crew.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such crew")
	case errors.Is(err, crew.ErrTooManyCrews):
		writeError(w, http.StatusConflict, "too_many_crews", err.Error())
	default:
		s.log.Error("crew save failed", "crew", id, "err", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "could not save the crews")
	}
}
