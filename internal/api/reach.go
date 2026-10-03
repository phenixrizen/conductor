package api

import (
	"net/http"

	"github.com/phenixrizen/conductor/internal/reach"
)

// reachSource says what the server knows about its reach (internal/reach).
type reachSource interface {
	Status() reach.Status
}

// certSource says whether the TLS listener has a certificate to serve: the
// discovered address becomes a link's base only then (internal/certs).
type certSource interface {
	Ready() bool
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
	TLS *tlsStatus `json:"tls,omitempty"`
}

// tlsStatus is the certificate part of the reach report; filled by the
// certificate manager when there is one.
type tlsStatus struct {
	Mode  string `json:"mode"`
	Ready bool   `json:"ready"`
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
		out.TLS = &tlsStatus{Mode: "files", Ready: certs.Ready()}
	}
	writeJSON(w, http.StatusOK, out)
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
