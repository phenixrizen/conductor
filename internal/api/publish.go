package api

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// Publisher publishes a server session to a rendezvous Conductor
// (hostagent.Uplink); PublishedSession is one publication.
type Publisher interface {
	Publish(ctx context.Context, local *session.Local) (PublishedSession, error)
	// Server is the rendezvous's URL, for a reply that says where a session
	// was meant to be published.
	Server() string
}

// PublishedSession is a session published to the rendezvous: its hooks get
// the local session's changes and entries, Stop ends the publication.
type PublishedSession interface {
	OnChange(info session.Info)
	OnActivity(id string, e session.ActivityEntry, state session.AttentionState)
	Stop()
	Base() string
	ID() string
	// Link mints a share link to the session at the rendezvous.
	Link(ctx context.Context, role string, ttl time.Duration, label string) (proto.LinkCreated, error)
	// Revoke revokes a link minted there; one it no longer knows answers an
	// error naming not_found.
	Revoke(ctx context.Context, linkID string) error
	// CurrentID is the rendezvous's id for the session now: ID unless the
	// rendezvous restarted and the session was registered afresh under
	// another id (an older rendezvous, which ignores host instances).
	CurrentID() string
	// HeldLinks is what the rendezvous last said it holds of the links
	// minted for the session; false when it never said (an older one).
	HeldLinks() ([]string, bool)
	// RunLink mints, at the rendezvous, one link to the sessions of a run's
	// members there; RunLinkUpdate tells it the members now. Either goes over
	// this session's connection, which must be a member's.
	RunLink(ctx context.Context, role string, ttl time.Duration, label string, run proto.RunGroup) (proto.LinkCreated, error)
	RunLinkUpdate(ctx context.Context, run proto.RunGroup) error
}

// remoteLink is a link minted at the rendezvous for a published session,
// kept here so the session's links list it and can revoke it; hostedID is
// the rendezvous's session id it was minted for, so a re-registration after
// a rendezvous restart drops it (the rendezvous keeps links in memory).
type remoteLink struct {
	ID        string
	Role      session.Role
	Label     string
	CreatedAt time.Time
	ExpiresAt *time.Time
	hostedID  string
}

// recordRemoteLink keeps a link minted at the rendezvous for session id.
func (s *Server) recordRemoteLink(id string, res proto.LinkCreated, hostedID string) {
	rl := remoteLink{ID: res.LinkID, Role: session.Role(res.Role), Label: res.Label, CreatedAt: time.Now(), hostedID: hostedID}
	if t, err := time.Parse(time.RFC3339, res.ExpiresAt); err == nil && res.ExpiresAt != "" {
		rl.ExpiresAt = &t
	}
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if s.remoteLinks == nil {
		s.remoteLinks = map[string]map[string]remoteLink{}
	}
	if s.remoteLinks[id] == nil {
		s.remoteLinks[id] = map[string]remoteLink{}
	}
	s.remoteLinks[id][res.LinkID] = rl
}

// remoteLinksOf lists the links minted at the rendezvous for session id that
// its current publication still holds, newest first; stale ones are dropped.
func (s *Server) remoteLinksOf(id string) []remoteLink {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	pub := s.published[id]
	var held []string
	known := false
	if pub != nil {
		held, known = pub.HeldLinks()
	}
	var out []remoteLink
	for lid, rl := range s.remoteLinks[id] {
		// Gone with the publication; else the rendezvous's word, when it
		// gives one; else (an older one) a session registered afresh lost them.
		gone := pub == nil
		switch {
		case gone:
		case known:
			gone = !slices.Contains(held, lid)
		default:
			gone = rl.hostedID != pub.CurrentID()
		}
		if gone {
			delete(s.remoteLinks[id], lid)
			continue
		}
		out = append(out, rl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// forgetRemoteLink drops the record of a remote link.
func (s *Server) forgetRemoteLink(id, linkID string) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	delete(s.remoteLinks[id], linkID)
}

// SetPublisher makes every server session created from now on published
// to the rendezvous; nil stops that.
func (s *Server) SetPublisher(p Publisher) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	s.publisher = p
}

// publish publishes local on a goroutine of the server's own: the dial to
// the rendezvous must not hold the launch. The result is recorded in the
// session's activity.
func (s *Server) publish(local *session.Local) {
	s.pubMu.Lock()
	p := s.publisher
	s.pubMu.Unlock()
	if p == nil {
		return
	}
	// A link request made meanwhile waits for this to end (awaitPublication).
	id := local.Info().ID
	pending := make(chan struct{})
	s.pubMu.Lock()
	if s.pubPending == nil {
		s.pubPending = map[string]chan struct{}{}
	}
	s.pubPending[id] = pending
	delete(s.pubErr, id)
	s.pubMu.Unlock()
	s.track(func() {
		defer func() {
			s.pubMu.Lock()
			delete(s.pubPending, id)
			s.pubMu.Unlock()
			close(pending)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pub, err := p.Publish(ctx, local)
		if err != nil {
			s.log.Warn("publish to the rendezvous failed", "session", id, "err", err.Error())
			local.Record(session.ActivityEntry{Type: session.ActivityError, Message: "not published to the rendezvous: " + err.Error()})
			s.pubMu.Lock()
			if s.pubErr == nil {
				s.pubErr = map[string]string{}
			}
			s.pubErr[id] = err.Error()
			s.pubMu.Unlock()
			return
		}
		s.pubMu.Lock()
		if s.published == nil {
			s.published = map[string]PublishedSession{}
		}
		gone := s.pubGone[local.Info().ID]
		if !gone {
			s.published[local.Info().ID] = pub
		}
		s.pubMu.Unlock()
		if gone {
			pub.Stop()
			return
		}
		url := pub.Base() + "/sessions/" + pub.ID()
		s.log.Info("published to the rendezvous", "session", local.Info().ID, "at", url)
		local.Record(session.ActivityEntry{Type: session.ActivityLink, Message: "published at the rendezvous", URL: url})
		// What changed before the publication reached the rendezvous.
		pub.OnChange(local.Info())
		// A member published now: its run's links there name its session.
		if runID, _, ok := s.runs.MemberOf(local.Info().ID); ok {
			s.scheduleRunSync(runID)
		}
	})
}

// awaitPublication is the publication of session id, waiting as long as ctx
// allows for one still being made; when there is none it says why: the
// publish's error, or that it is still connecting.
func (s *Server) awaitPublication(ctx context.Context, id string) (PublishedSession, string) {
	s.pubMu.Lock()
	pub, pending, why := s.published[id], s.pubPending[id], s.pubErr[id]
	s.pubMu.Unlock()
	if pub != nil {
		return pub, ""
	}
	if pending != nil {
		select {
		case <-pending:
		case <-ctx.Done():
			return nil, "still connecting to the switchyard"
		}
		s.pubMu.Lock()
		pub, why = s.published[id], s.pubErr[id]
		s.pubMu.Unlock()
		if pub != nil {
			return pub, ""
		}
	}
	if why == "" {
		why = "not published"
	}
	return nil, why
}

// publisherServer is the rendezvous sessions are published to, "" for none.
func (s *Server) publisherServer() string {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if s.publisher == nil {
		return ""
	}
	return s.publisher.Server()
}

// publishedOf is the publication of a session, nil for none.
func (s *Server) publishedOf(id string) PublishedSession {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	return s.published[id]
}

// unpublish ends a session's publication when it leaves the registry.
func (s *Server) unpublish(id string) {
	s.pubMu.Lock()
	pub := s.published[id]
	delete(s.published, id)
	delete(s.remoteLinks, id)
	delete(s.pubErr, id)
	if s.publisher != nil {
		if s.pubGone == nil {
			s.pubGone = map[string]bool{}
		}
		s.pubGone[id] = true
	}
	s.pubMu.Unlock()
	if pub != nil {
		pub.Stop()
	}
}

// localActivity is the OnActivity hook of a server session: the entry goes
// to the event hub (and through it to the runs and the webhooks), and to the
// rendezvous when the session is published. Neither waits.
func (s *Server) localActivity(id string, e session.ActivityEntry, state session.AttentionState) {
	s.events.activity(id, e, state)
	if pub := s.publishedOf(id); pub != nil {
		pub.OnActivity(id, e, state)
	}
}

// endLinks takes a session's links with it when it ends: there is nothing
// left to share. Its links here are revoked (closing whoever used them) and
// forgotten; those minted at the rendezvous are revoked there, in the
// background and within a few seconds each, and forgotten here whatever the
// rendezvous answers. Calling it again finds nothing to do.
func (s *Server) endLinks(id string) {
	for _, l := range s.links.ListBySession(id) {
		s.links.Revoke(id, l.ID)
	}
	s.links.DeleteSession(id, true)
	s.pubMu.Lock()
	pub := s.published[id]
	remote := s.remoteLinks[id]
	delete(s.remoteLinks, id)
	s.pubMu.Unlock()
	if pub == nil || len(remote) == 0 {
		return
	}
	s.track(func() {
		for lid := range remote {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = pub.Revoke(ctx, lid)
			cancel()
		}
	})
}
