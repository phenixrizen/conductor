package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

type createLinkRequest struct {
	Role       session.Role `json:"role"`
	Label      string       `json:"label"`
	TTLSeconds int64        `json:"ttlSeconds"`
}

// linkView is a share link plus the number of viewers attached through it.
type linkView struct {
	*share.Link
	Active int `json:"active"`
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	active := d.LinkViewers()
	links := s.links.ListBySession(id)
	out := make([]linkView, 0, len(links))
	for _, l := range links {
		out = append(out, linkView{Link: l, Active: active[l.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	req, ok := readLinkRequest(w, r)
	if !ok {
		return
	}
	// A session published to a rendezvous is viewed there: its links are
	// minted there, over the host connection.
	if pub := s.publishedOf(id); pub != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		res, err := pub.Link(ctx, string(req.Role), req.ttl(), req.Label)
		if err != nil {
			writeError(w, http.StatusBadGateway, "rendezvous_unavailable", "the rendezvous did not mint the link: "+err.Error())
			return
		}
		s.recordLink(id, "link created at the rendezvous: "+linkLabelOr(res.Label)+" ("+res.Role+")")
		writeJSON(w, http.StatusCreated, map[string]any{
			"link":   map[string]any{"id": res.LinkID, "role": res.Role, "label": res.Label, "expiresAt": res.ExpiresAt, "sessionId": id, "remote": true},
			"url":    res.URL,
			"invite": res.Invite,
			"remote": true,
		})
		return
	}
	link, token, err := s.links.Create(id, req.Role, req.Label, req.ttl())
	if err != nil {
		writeLinkError(w, err, err.Error())
		return
	}
	s.recordLink(id, "link created: "+linkLabelOr(link.Label)+" ("+string(link.Role)+")")
	s.writeLink(w, r, link, token)
}

// readLinkRequest reads and checks the body of a link creation. It writes the
// response and returns false when the body is refused.
func readLinkRequest(w http.ResponseWriter, r *http.Request) (createLinkRequest, bool) {
	var req createLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return req, false
	}
	if !req.Role.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_role", "role must be view or control")
		return req, false
	}
	if req.TTLSeconds < 0 || req.TTLSeconds > 365*24*3600 {
		writeError(w, http.StatusBadRequest, "invalid_request", "ttlSeconds out of range")
		return req, false
	}
	return req, true
}

func (req createLinkRequest) ttl() time.Duration { return time.Duration(req.TTLSeconds) * time.Second }

// writeLinkError answers a link the store would not create; tooMany is the
// message for share.ErrTooManyLinks.
func writeLinkError(w http.ResponseWriter, err error, tooMany string) {
	if errors.Is(err, share.ErrTooManyLinks) {
		writeError(w, http.StatusConflict, "too_many_links", tooMany)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", "could not create link")
}

// writeLink answers a link just created: 201 {link, token, url}, the token
// shown this once and the URL the join page's.
func (s *Server) writeLink(w http.ResponseWriter, r *http.Request, link *share.Link, token string) {
	writeJSON(w, http.StatusCreated, s.linkReply(r, link, token))
}

// linkReply is a link just created as a reply carries it: {link, token, url},
// the URL on the base publicBase gives for r.
func (s *Server) linkReply(r *http.Request, link *share.Link, token string) map[string]any {
	base := s.publicBase(r)
	return map[string]any{
		"link":   link,
		"token":  token,
		"url":    base + "/join/" + url.PathEscape(token),
		"invite": inviteFor(base, token),
	}
}

// forwardedHost matches what a Host header or a reverse proxy's
// X-Forwarded-Host may carry: a host name or address, with a port.
var forwardedHost = regexp.MustCompile(`^[A-Za-z0-9.\-]+(?::\d{1,5})?$|^\[[0-9A-Fa-f:.]+\](?::\d{1,5})?$`)

// publicBase is the base URL a share link made for r is built on: publicUrl
// when it names another machine; otherwise (unset, or localhost, as the
// default and the example config have it) the public address reach found
// and mapped, once a certificate for it is ready (discoveredBase); otherwise
// the address r came through, which is the address the workbench was opened
// at: the scheme r arrived with (or the one a reverse proxy sets in
// X-Forwarded-Proto) and r's Host (or X-Forwarded-Host). A request whose
// host is missing or malformed falls back to publicUrl.
func (s *Server) publicBase(r *http.Request) string {
	if !s.cfg.PublicURLIsLocal() {
		return s.cfg.PublicURL
	}
	if base := s.discoveredBase(); base != "" {
		return base
	}
	if r == nil {
		return s.cfg.PublicURL
	}
	first := func(header string) string {
		return strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get(header), ",")[0]))
	}
	host := first("X-Forwarded-Host")
	if host == "" {
		host = strings.ToLower(r.Host)
	}
	if !forwardedHost.MatchString(host) {
		return s.cfg.PublicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := first("X-Forwarded-Proto"); p == "http" || p == "https" {
		scheme = p
	}
	return scheme + "://" + host
}

// handleCreateRunLink creates a link to a crew run: its role on the session
// of every member of the run, those that join later included.
func (s *Server) handleCreateRunLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	if _, ok := s.runs.Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	req, ok := readLinkRequest(w, r)
	if !ok {
		return
	}
	// The link is made under the check that the run is kept: a run forgotten
	// in between takes its links (OnForget), and this one would be left over.
	var (
		link  *share.Link
		token string
		err   error
	)
	if !s.runs.IfKept(id, func() { link, token, err = s.links.CreateRunLink(id, req.Role, req.Label, req.ttl()) }) {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	if err != nil {
		writeLinkError(w, err, "too many links for this run")
		return
	}
	s.runs.Note(id, session.ActivityLink, "link created: "+linkLabelOr(link.Label)+" ("+string(link.Role)+")")
	s.writeLink(w, r, link, token)
}

// handleListRunLinks lists a run's links, each with the viewers attached
// through it to the sessions of the run's members.
func (s *Server) handleListRunLinks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	run, ok := s.runs.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	active := map[string]int{}
	for _, m := range run.Members {
		if m.SessionID == "" {
			continue
		}
		if d, ok := s.registry.Get(m.SessionID); ok {
			for linkID, n := range d.LinkViewers() {
				active[linkID] += n
			}
		}
	}
	links := s.links.ListByRun(id)
	out := make([]linkView, 0, len(links))
	for _, l := range links {
		out = append(out, linkView{Link: l, Active: active[l.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// handleRevokeRunLink revokes a run's link, which closes every viewer
// attached through it, and notes it in the run's log.
func (s *Server) handleRevokeRunLink(w http.ResponseWriter, r *http.Request) {
	id, linkID := r.PathValue("run"), r.PathValue("linkId")
	label := ""
	if l, ok := s.links.Get(linkID); ok {
		label = l.Label
	}
	found, revoked := s.links.RevokeRun(id, linkID)
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "no such link")
		return
	}
	if revoked {
		s.runs.Note(id, session.ActivityLink, "link revoked: "+linkLabelOr(label))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevokeLink(w http.ResponseWriter, r *http.Request) {
	id, linkID := r.PathValue("id"), r.PathValue("linkId")
	label := ""
	if l, ok := s.links.Get(linkID); ok {
		label = l.Label
	}
	found, revoked := s.links.Revoke(id, linkID)
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "no such link")
		return
	}
	if revoked {
		s.recordLink(id, "link revoked: "+linkLabelOr(label))
	}
	w.WriteHeader(http.StatusNoContent)
}

// recordLink adds a link event to a server session's activity log. Hosted
// sessions keep their log on the host, which never learns about links.
func (s *Server) recordLink(sessionID, message string) {
	if d, ok := s.registry.Get(sessionID); ok {
		if l, ok := d.(*session.Local); ok {
			l.Record(session.ActivityEntry{Type: session.ActivityLink, Message: message})
		}
	}
}

func linkLabelOr(label string) string {
	if label == "" {
		return "unlabelled"
	}
	return label
}

// handleJoin resolves a share token for the join page.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientKey(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	link, err := s.links.Resolve(r.PathValue("token"))
	if err != nil {
		code := "invalid_link"
		switch {
		case errors.Is(err, share.ErrRevoked):
			code = "revoked"
		case errors.Is(err, share.ErrExpired):
			code = "expired"
		}
		writeError(w, http.StatusNotFound, code, "this link is not valid")
		return
	}
	if link.RunID != "" {
		s.joinRun(w, link)
		return
	}
	d, ok := s.registry.Get(link.SessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "session_gone", "the shared session no longer exists")
		return
	}
	info := d.Info()
	writeJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id":       info.ID,
			"name":     info.Name,
			"agentId":  info.AgentID,
			"kind":     info.Kind,
			"status":   info.Status,
			"cols":     info.Cols,
			"rows":     info.Rows,
			"hostName": info.HostName,
			"hostUser": info.HostUser,
		},
		"role":  link.Role,
		"label": link.Label,
	})
}

// joinMember is a member of a run as the join page sees it: SessionID,
// AgentID and Status those of its session while that runs; otherwise its
// state in the run, pending, starting or ended, and no session.
type joinMember struct {
	Name      string `json:"name"`
	SessionID string `json:"sessionId,omitempty"`
	AgentID   string `json:"agentId"`
	Status    string `json:"status"`
}

// joinRun answers the join page for a run link: the run and its members.
func (s *Server) joinRun(w http.ResponseWriter, link *share.Link) {
	run, ok := s.runs.Get(link.RunID)
	if !ok {
		writeError(w, http.StatusNotFound, "run_gone", "the shared run no longer exists")
		return
	}
	members := make([]joinMember, 0, len(run.Members))
	for _, m := range run.Members {
		jm := joinMember{Name: m.Name, AgentID: m.AgentID, Status: m.Status}
		if m.SessionID != "" {
			jm.Status = crew.MemberEnded
			if d, ok := s.registry.Get(m.SessionID); ok && m.Status != crew.MemberEnded {
				if info := d.Info(); !info.Status.Ended() {
					jm.SessionID, jm.AgentID, jm.Status = info.ID, info.AgentID, string(info.Status)
				}
			}
		}
		members = append(members, jm)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":   map[string]any{"id": run.ID, "name": run.Name, "members": members},
		"role":  link.Role,
		"label": link.Label,
	})
}
