package api

import (
	"context"
	"time"

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
