package api

import (
	"net/http"

	"github.com/phenixrizen/conductor/internal/catalog"
)

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	list := s.catalog.List()
	out := make([]catalog.Agent, 0, len(list))
	for _, a := range list {
		out = append(out, a.Redacted())
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}
