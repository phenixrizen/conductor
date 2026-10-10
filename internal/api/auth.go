package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

// principal is the authenticated caller of a request.
type principal struct {
	admin bool
	host  bool
	link  *share.Link
	// runOf answers the run a session is a member of, for a run link
	// (Server.runOf).
	runOf func(sessionID string) (runID string, ok bool)
}

// role returns the terminal role the principal has on sessionID, or "" when
// it has no access. A session's link grants its role on that session; a
// run's link on the session of every member of the run, whenever it joined.
func (p principal) role(sessionID string) session.Role {
	if p.admin {
		return session.RoleControl
	}
	if p.link == nil || sessionID == "" {
		return ""
	}
	if p.link.RunID == "" {
		if p.link.SessionID == sessionID {
			return p.link.Role
		}
		return ""
	}
	// A host's run link on a switchyard names its members' sessions itself.
	if p.link.Group != nil {
		if p.link.Group.Names(sessionID) {
			return p.link.Role
		}
		return ""
	}
	if p.runOf != nil {
		if runID, ok := p.runOf(sessionID); ok && runID == p.link.RunID {
			return p.link.Role
		}
	}
	return ""
}

func (p principal) linkID() string {
	if p.link != nil {
		return p.link.ID
	}
	return ""
}

// attachCheck is the check a viewer's attach to sessionID makes again, under
// the session's lock, in the critical section that registers the viewer
// (session.AttachOptions.Authorize, signal.ViewerOptions.Authorize): the
// principal's link as it stands then, and the role it gives on the session
// then (its run's members, a switchyard group's sessions). A link revoked,
// expired or forgotten since the token was presented refuses the attach.
// Nil for a principal without a link: the workbench token is not revoked.
// It reads the share store and the run index only, neither of which calls a
// session, so it is safe under a session's lock.
func (s *Server) attachCheck(p principal, sessionID string) func() (session.Grant, error) {
	if p.link == nil {
		return nil
	}
	linkID := p.link.ID
	return func() (session.Grant, error) {
		l, err := s.links.Live(linkID)
		if err != nil {
			if errors.Is(err, share.ErrExpired) {
				return session.Grant{}, session.ErrExpired
			}
			return session.Grant{}, session.ErrRevoked
		}
		role := principal{link: l, runOf: s.runOf}.role(sessionID)
		if role == "" {
			return session.Grant{}, session.ErrRevoked
		}
		g := session.Grant{Role: role}
		if l.ExpiresAt != nil {
			g.Until = *l.ExpiresAt
		}
		return g, nil
	}
}

// presentedToken extracts the credential from the Authorization header or,
// for WebSocket and join routes that browsers cannot add headers to, from the
// token query parameter.
func presentedToken(r *http.Request, allowQuery bool) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(tok)
		}
		return ""
	}
	if allowQuery {
		return r.URL.Query().Get("token")
	}
	return ""
}

// authenticate resolves a presented token to a principal. Unknown tokens
// yield an empty principal; callers decide the response.
func (s *Server) authenticate(r *http.Request, allowQuery bool) principal {
	tok := presentedToken(r, allowQuery)
	if tok == "" {
		return principal{}
	}
	if share.Equal(tok, s.cfg.WorkbenchToken) {
		return principal{admin: true, host: true}
	}
	for _, ht := range s.cfg.HostTokens {
		if share.Equal(tok, ht) {
			return principal{host: true}
		}
	}
	if link, err := s.links.Resolve(tok); err == nil {
		return principal{link: link, runOf: s.runOf}
	} else if !errors.Is(err, share.ErrUnknownToken) {
		s.log.Debug("share token rejected", "reason", err)
	}
	return principal{}
}

// requireAdmin wraps handlers that need the workbench token (the admin principal).
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticate(r, false).admin {
			if !s.limiter.allow(clientKey(r)) {
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="conductor"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "workbench token required")
			return
		}
		next(w, r)
	}
}
