package api

import (
	"encoding/hex"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
	"github.com/phenixrizen/conductor/internal/store"
)

// A switchyard keeps the links it mints for hosts that register under an
// instance (proto.HostInfo.Instance) in links/ of its data directory, one
// document per link: the hash of its token (never the token), what the join
// page shows, and the session's id, which the same host gets again when it
// registers after a restart. So a restart loses no link; until the host is
// back a join says so (503 host_offline). A revoked link's file goes at once,
// an expired one at the next sweep, and a link whose host has not registered
// for linkOrphanAfter with it.

// linkFile is a link as a switchyard keeps it.
type linkFile struct {
	ID        string `json:"id"`
	TokenHash string `json:"tokenHash"`
	SessionID string `json:"sessionId,omitempty"`
	// RunID and Run are a host's run link's: its run, and its members as the host last told them.
	RunID     string       `json:"runId,omitempty"`
	Run       *share.Group `json:"run,omitempty"`
	Role      session.Role `json:"role"`
	Label     string       `json:"label,omitempty"`
	CreatedAt time.Time    `json:"createdAt"`
	ExpiresAt *time.Time   `json:"expiresAt,omitempty"`
	Owner     string       `json:"owner"`
	Addr      string       `json:"addr,omitempty"`
}

const (
	// maxLinkFile bounds one file as it is read back.
	maxLinkFile = 16 << 10
	// maxDurableLinks bounds the links a switchyard keeps in all: past it, it mints none.
	maxDurableLinks = 20000
	// linkOrphanAfter is how long a kept link waits for its host to register again.
	linkOrphanAfter = 7 * 24 * time.Hour
)

// linkKeeper holds what keeping links needs beside the store: the files, how
// many each open-host address holds, and when each host instance last registered.
type linkKeeper struct {
	files *store.Store
	mu    sync.Mutex
	addrs map[string]int
	// counted are the links counted in addrs, by id, with the address each
	// counts against: a link leaves its count once, however often it is
	// dropped (a revoke tells its hook each time, an expiry and the sweep both).
	counted map[string]string
	seen    map[string]time.Time
	// hashes are the kept links' token hashes (hex), for writing a file again.
	hashes map[string]string
}

// openLinkKeeper opens links/ under st and puts the links kept there back
// into links: expired ones are deleted, an unreadable one logged and left
// out, never fatal.
func (s *Server) openLinkKeeper(st *store.Store) {
	files, err := st.Sub("links")
	if err != nil {
		s.log.Error("links are not kept across restarts: cannot open the links directory", "err", err)
		return
	}
	k := &linkKeeper{files: files, addrs: map[string]int{}, counted: map[string]string{}, seen: map[string]time.Time{}, hashes: map[string]string{}}
	s.keeper = k
	entries, err := files.List()
	if err != nil {
		s.log.Error("cannot list the kept links", "err", err)
		return
	}
	now := time.Now()
	restored := 0
	for _, e := range entries {
		var f linkFile
		if ok, err := files.LoadLimit(e.Name, &f, maxLinkFile); !ok || err != nil {
			s.log.Error("a kept link is left out", "file", e.Name, "err", err)
			continue
		}
		raw, err := hex.DecodeString(f.TokenHash)
		if err != nil || len(raw) != len(share.Hash{}) || f.ID == "" || (f.SessionID == "") == (f.RunID == "" || f.Run == nil) || f.Owner == "" || !f.Role.Valid() {
			s.log.Error("a kept link is left out", "file", e.Name)
			continue
		}
		if f.ExpiresAt != nil && !now.Before(*f.ExpiresAt) {
			_ = files.Delete(e.Name)
			continue
		}
		var h share.Hash
		copy(h[:], raw)
		s.links.Restore(share.Link{ID: f.ID, SessionID: f.SessionID, RunID: f.RunID, Group: f.Run, Role: f.Role, Label: f.Label, CreatedAt: f.CreatedAt, ExpiresAt: f.ExpiresAt, Owner: f.Owner, Addr: f.Addr}, h)
		if f.Addr != "" {
			k.addrs[f.Addr]++
			k.counted[f.ID] = f.Addr
		}
		k.hashes[f.ID] = f.TokenHash
		k.seen[f.Owner] = now
		restored++
	}
	if restored > 0 {
		s.log.Info("kept links restored", "links", restored)
	}
}

// keepLink makes a link minted for a hosted session of owner durable and
// writes its file, within the bounds; false (and the link revoked) when
// they leave no room.
func (s *Server) keepLink(link *share.Link, token, owner, addr string) bool {
	k := s.keeper
	if k == nil || owner == "" {
		return true
	}
	k.mu.Lock()
	full := s.links.Count() > maxDurableLinks || (addr != "" && k.addrs[addr] >= s.cfg.Switchyard.OpenHostLinks)
	if !full && addr != "" {
		k.addrs[addr]++
		k.counted[link.ID] = addr
	}
	k.mu.Unlock()
	if full {
		s.links.Drop(link.ID)
		return false
	}
	s.links.SetOwner(link.ID, owner, addr)
	h := share.HashToken(token)
	k.mu.Lock()
	k.hashes[link.ID] = hex.EncodeToString(h[:])
	k.mu.Unlock()
	link.Owner, link.Addr = owner, addr
	s.writeLinkFile(link)
	return true
}

// writeLinkFile writes a kept link's file as the store holds it now.
func (s *Server) writeLinkFile(link *share.Link) {
	k := s.keeper
	k.mu.Lock()
	hash := k.hashes[link.ID]
	k.mu.Unlock()
	if hash == "" {
		return
	}
	f := linkFile{ID: link.ID, TokenHash: hash, SessionID: link.SessionID, Role: link.Role, Label: link.Label, CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt, Owner: link.Owner, Addr: link.Addr}
	if link.Group != nil {
		f.RunID, f.Run = link.RunID, link.Group
	}
	if err := k.files.Save(link.ID+".json", f); err != nil {
		s.log.Error("a link is not kept across restarts", "link", link.ID, "err", err)
	}
}

// keepGroup writes again the files of owner's run links, whose members changed.
func (s *Server) keepGroup(owner, runID string) {
	if s.keeper == nil {
		return
	}
	for _, l := range s.links.Durable() {
		if l.Owner == owner && l.RunID == runID && l.Group != nil && !l.Revoked {
			s.writeLinkFile(l)
		}
	}
}

// dropLinkFile forgets a kept link's file and its place in its address's
// count. Calling it again for the same link changes nothing.
func (s *Server) dropLinkFile(linkID string) {
	k := s.keeper
	if k == nil {
		return
	}
	k.mu.Lock()
	if addr, ok := k.counted[linkID]; ok {
		delete(k.counted, linkID)
		if k.addrs[addr]--; k.addrs[addr] <= 0 {
			delete(k.addrs, addr)
		}
	}
	delete(k.hashes, linkID)
	k.mu.Unlock()
	_ = k.files.Delete(linkID + ".json")
}

// sawHost notes that a host instance registered: its kept links wait for it.
func (s *Server) sawHost(owner string) {
	if k := s.keeper; k != nil && owner != "" {
		k.mu.Lock()
		k.seen[owner] = time.Now()
		k.mu.Unlock()
	}
}

// sweepLinks drops the kept links that expired, and those whose host has not
// registered for linkOrphanAfter, closing whoever is attached through them.
func (s *Server) sweepLinks(now time.Time) {
	k := s.keeper
	if k == nil {
		return
	}
	for _, l := range s.links.Durable() {
		k.mu.Lock()
		orphan := now.Sub(k.seen[l.Owner]) > linkOrphanAfter
		k.mu.Unlock()
		if orphan || (l.ExpiresAt != nil && !now.Before(*l.ExpiresAt)) {
			s.dropLinkFile(l.ID)
			s.links.Drop(l.ID)
			// Dropped, the link opens nothing (Live): whoever is still
			// attached through it goes too, as on a revoke.
			s.closeLinkViewers(l)
		}
	}
}
