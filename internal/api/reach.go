package api

import (
	"net/http"

	"github.com/phenixrizen/conductor/internal/certs"
	"github.com/phenixrizen/conductor/internal/reach"
)

// reachSource says what the server knows about its reach (internal/reach).
type reachSource interface {
	Status() reach.Status
}

// certSource is the TLS listener's certificate manager (internal/certs):
// the discovered address becomes a link's base only once it is ready, and
// its open http-01 challenges are served on the plain listener.
type certSource interface {
	Ready() bool
	Status() certs.Status
	HTTP01(token string) (keyAuth string, ok bool)
}

// SetReach gives the server its reach status and certificate readiness;
// either may be nil (no mapper, no TLS).
func (s *Server) SetReach(r reachSource, c certSource) {
	s.reachMu.Lock()
	defer s.reachMu.Unlock()
	s.reach, s.certs = r, c
}

// Instance identifies this server process; the reach self-check matches it
// on /api/health.
func (s *Server) Instance() string { return s.instance }

// reachStatus is what GET /api/reach reports.
type reachStatus struct {
	reach.Status
	TLS *certs.Status `json:"tls,omitempty"`
}

func (s *Server) handleReach(w http.ResponseWriter, r *http.Request) {
	s.reachMu.Lock()
	src, certs := s.reach, s.certs
	s.reachMu.Unlock()
	out := reachStatus{Status: reach.Status{Mode: reach.ModeOff}}
	if src != nil {
		out.Status = src.Status()
	}
	if certs != nil {
		st := certs.Status()
		out.TLS = &st
	}
	writeJSON(w, http.StatusOK, out)
}

// handleACMEChallenge answers an http-01 challenge of an open order on the
// plain listener (RFC 8555 §8.3); 404 for any other token, and without a
// certificate manager.
func (s *Server) handleACMEChallenge(w http.ResponseWriter, r *http.Request) {
	s.reachMu.Lock()
	certs := s.certs
	s.reachMu.Unlock()
	token := r.PathValue("token")
	if certs == nil || len(token) > 128 {
		http.NotFound(w, r)
		return
	}
	keyAuth, ok := certs.HTTP01(token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(keyAuth))
}

// discoveredBase is the base share links take from reach: the public URL of
// a mapped (or manually forwarded) port, once a certificate for it is ready;
// "" otherwise, so links fall back to the request's address. Plain http is
// never advertised on the public address.
func (s *Server) discoveredBase() string {
	s.reachMu.Lock()
	src, certs := s.reach, s.certs
	s.reachMu.Unlock()
	if src == nil || certs == nil || !certs.Ready() {
		return ""
	}
	st := src.Status()
	if st.PublicURL == "" || (!st.Mapped && st.Method != reach.MethodManual) {
		return ""
	}
	return st.PublicURL
}

// notifyBase is the base of the notify URL agents report to: always the
// configured publicUrl (local by default), never the discovered address,
// which a router that does not hairpin would refuse from inside.
func (s *Server) notifyBase() string { return s.cfg.PublicURL }
