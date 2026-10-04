package api

import (
	"context"
	"sort"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// Publisher publishes a server session to a rendezvous Conductor
// (hostagent.Uplink); PublishedSession is one publication.
type Publisher interface {
	Publish(ctx context.Context, local *session.Local) (PublishedSession, error)
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
	// rendezvous restarted and the session was registered afresh, which
	// loses the links minted before.
	CurrentID() string
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
	var out []remoteLink
	for lid, rl := range s.remoteLinks[id] {
		if pub == nil || rl.hostedID != pub.CurrentID() {
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
	s.track(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pub, err := p.Publish(ctx, local)
		if err != nil {
			s.log.Warn("publish to the rendezvous failed", "session", local.Info().ID, "err", err.Error())
			local.Record(session.ActivityEntry{Type: session.ActivityError, Message: "not published to the rendezvous: " + err.Error()})
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
	})
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
