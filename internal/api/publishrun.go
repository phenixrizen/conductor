package api

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// A crew run's link through the switchyard: the switchyard runs no crews,
// so this server tells it the run's members (a proto.RunGroup, each member's
// session as published there) over any member's publication (the carrier)
// and keeps it current as members start, end or join (link_run_update), and
// keeps a record of each such link for the run's Share dialog, as it does a
// session's remote links.

// runSyncDelay coalesces a run's changes before the switchyard is told: the
// engine reports a change on every log line.
const runSyncDelay = 2 * time.Second

// runGroup is the run as the switchyard should show it, and a publication to
// carry requests about it (nil when no member is published).
func (s *Server) runGroup(run crew.Run) (proto.RunGroup, PublishedSession) {
	g := proto.RunGroup{ID: run.ID, Name: cmpLabel(run.Label, run.Name)}
	var carrier PublishedSession
	for _, m := range run.Members {
		pm := proto.RunMember{Name: m.Name, AgentID: m.AgentID, Status: m.Status}
		if m.SessionID != "" {
			if pub := s.publishedOf(m.SessionID); pub != nil {
				pm.SessionID = pub.CurrentID()
				if carrier == nil {
					carrier = pub
				}
			}
		}
		g.Members = append(g.Members, pm)
	}
	if len(g.Members) > proto.MaxRunLinkMembers {
		g.Members = g.Members[:proto.MaxRunLinkMembers]
	}
	return g, carrier
}

func cmpLabel(label, name string) string {
	if label != "" {
		return label
	}
	return name
}

// awaitRunGroup is runGroup once the member publications still being made
// are made, as long as ctx allows; why says what keeps a run from the
// switchyard when no member is published.
func (s *Server) awaitRunGroup(ctx context.Context, runID string) (proto.RunGroup, PublishedSession, string) {
	run, ok := s.runs.Get(runID)
	if !ok {
		return proto.RunGroup{}, nil, "no such run"
	}
	why := ""
	for _, m := range run.Members {
		if m.SessionID == "" {
			continue
		}
		if _, w := s.awaitPublication(ctx, m.SessionID); w != "" && why == "" {
			why = w
		}
	}
	run, _ = s.runs.Get(runID)
	g, carrier := s.runGroup(run)
	if carrier == nil && why == "" {
		why = "no member of the run is published yet"
	}
	return g, carrier, why
}

// recordRemoteRunLink keeps a link minted at the rendezvous for a run.
func (s *Server) recordRemoteRunLink(runID string, res proto.LinkCreated) {
	rl := remoteLink{ID: res.LinkID, Role: session.Role(res.Role), Label: res.Label, CreatedAt: time.Now()}
	if t, err := time.Parse(time.RFC3339, res.ExpiresAt); err == nil && res.ExpiresAt != "" {
		rl.ExpiresAt = &t
	}
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if s.remoteRunLinks == nil {
		s.remoteRunLinks = map[string]map[string]remoteLink{}
	}
	if s.remoteRunLinks[runID] == nil {
		s.remoteRunLinks[runID] = map[string]remoteLink{}
	}
	s.remoteRunLinks[runID][res.LinkID] = rl
}

// remoteRunLinksOf lists the records of a run's links minted at the rendezvous, newest first.
func (s *Server) remoteRunLinksOf(runID string) []remoteLink {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	out := make([]remoteLink, 0, len(s.remoteRunLinks[runID]))
	for _, rl := range s.remoteRunLinks[runID] {
		out = append(out, rl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// forgetRemoteRunLinks drops one record of a run's, or all of them ("").
func (s *Server) forgetRemoteRunLinks(runID, linkID string) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if linkID == "" {
		delete(s.remoteRunLinks, runID)
		delete(s.runSent, runID)
		return
	}
	delete(s.remoteRunLinks[runID], linkID)
}

// scheduleRunSync tells the switchyard a run's members after runSyncDelay,
// once for the changes in between; nothing for a run without remote links.
func (s *Server) scheduleRunSync(runID string) {
	s.pubMu.Lock()
	if len(s.remoteRunLinks[runID]) == 0 || s.runSyncing[runID] {
		s.pubMu.Unlock()
		return
	}
	if s.runSyncing == nil {
		s.runSyncing = map[string]bool{}
	}
	s.runSyncing[runID] = true
	s.pubMu.Unlock()
	time.AfterFunc(runSyncDelay, func() {
		s.pubMu.Lock()
		delete(s.runSyncing, runID)
		s.pubMu.Unlock()
		s.track(func() { s.syncRunLinks(runID) })
	})
}

// syncRunLinks sends the run's members to the switchyard when they changed
// since the last time; a switchyard that holds no link for the run any more
// (not_found) has the records dropped here too.
func (s *Server) syncRunLinks(runID string) {
	run, ok := s.runs.Get(runID)
	if !ok {
		return
	}
	g, carrier := s.runGroup(run)
	if carrier == nil {
		return
	}
	b, _ := json.Marshal(g)
	s.pubMu.Lock()
	same := s.runSent[runID] == string(b)
	s.pubMu.Unlock()
	if same {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := carrier.RunLinkUpdate(ctx, g); err != nil {
		if strings.Contains(err.Error(), "not_found") {
			s.forgetRemoteRunLinks(runID, "")
		}
		return
	}
	s.pubMu.Lock()
	if s.runSent == nil {
		s.runSent = map[string]string{}
	}
	s.runSent[runID] = string(b)
	s.pubMu.Unlock()
}

// hasRemoteRunLink says whether a run has a record of the link.
func (s *Server) hasRemoteRunLink(runID, linkID string) bool {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	_, ok := s.remoteRunLinks[runID][linkID]
	return ok
}
