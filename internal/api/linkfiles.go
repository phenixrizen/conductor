package api

import (
	"cmp"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
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
// an expired one at the next sweep, and a link whose host has been gone for
// linkOrphanAfter with it (a host connected is never gone, however long ago
// it registered). An open host's link also gives its place in its address's
// count to a new link of that address once its host has been gone for
// linkYieldAfter and the address has no other room: it is ended then.

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
	// linkOrphanAfter is how long a kept link waits for its host to come back.
	linkOrphanAfter = 7 * 24 * time.Hour
	// linkYieldAfter is how long an open host's kept link holds its place in
	// its address's count once its host has gone, whatever else the address
	// needs: well past what a host's reconnect takes (its retries are 30 s
	// apart at most; after a restart the switchyard counts from its start),
	// so a host that only lost its connection finds its links as it left
	// them. Past it the link keeps its place until a new link of the
	// address has no other.
	linkYieldAfter = 5 * time.Minute
)

// keptLink is what the keeper holds of a kept link beside the store.
type keptLink struct {
	// hash is its token's hash (hex), for writing its file again.
	hash    string
	owner   string
	addr    string
	created time.Time
}

// linkKeeper holds what keeping links needs beside the store: the files, the
// links counted against each open-host address, and whether each host
// instance is connected or since when it is gone.
//
// Locks go in this order: fmu, then mu, then the link store's own.
type linkKeeper struct {
	files *store.Store
	// fmu orders the files' saves and deletes. A save looks, under it,
	// whether its link is still kept and still opens anything, and a link
	// stops being kept under it before its file goes: no save of a link's
	// file lands after its delete (a run update writing its run's links
	// again while one of them is revoked), so a revoked link is never back
	// after a restart.
	fmu sync.Mutex
	mu  sync.Mutex
	// kept are the kept links, by id. A link leaves them, and its
	// address's count, once, however often it is dropped (a revoke tells
	// its hook each time, an expiry and the sweep both).
	kept map[string]keptLink
	// addrs are the ids of the links counted against each open-host address.
	addrs map[string]map[string]struct{}
	// live counts each host instance's connections; seen is when an
	// instance was last connected (as it registered or left; for the owners
	// of the links restored at startup, the start).
	live map[string]int
	seen map[string]time.Time
	// undeleted are the links no longer kept whose file could not be
	// deleted (its directory not writable for a while): each sweep tries
	// again, so the file does not bring the link back at the next start.
	// Under fmu.
	undeleted map[string]struct{}
}

// deleteFile deletes the file of a link no longer kept, or notes it for the
// sweep to try again. Called with fmu held.
func (s *Server) deleteFile(id string) {
	k := s.keeper
	if err := k.files.Delete(id + ".json"); err != nil {
		if _, again := k.undeleted[id]; !again {
			s.log.Error("a link's file is not deleted yet; the sweep tries again", "link", id, "err", err)
		}
		k.undeleted[id] = struct{}{}
		return
	}
	delete(k.undeleted, id)
}

// away is how long the host instance owner has been gone at now: none while
// it holds a connection. Called with mu held.
func (k *linkKeeper) away(owner string, now time.Time) time.Duration {
	if k.live[owner] > 0 {
		return 0
	}
	return now.Sub(k.seen[owner])
}

// hold counts a kept link in. Called with mu held.
func (k *linkKeeper) hold(id string, kl keptLink) {
	k.kept[id] = kl
	if kl.addr != "" {
		if k.addrs[kl.addr] == nil {
			k.addrs[kl.addr] = map[string]struct{}{}
		}
		k.addrs[kl.addr][id] = struct{}{}
	}
}

// release forgets a kept link: its hash and its place in its address's
// count. Releasing it again changes nothing. Called with mu held.
func (k *linkKeeper) release(id string) {
	kl, ok := k.kept[id]
	if !ok {
		return
	}
	delete(k.kept, id)
	if kl.addr != "" {
		delete(k.addrs[kl.addr], id)
		if len(k.addrs[kl.addr]) == 0 {
			delete(k.addrs, kl.addr)
		}
	}
}

// yielding is the n links counted against addr that give their places up
// first: of those whose host has been gone longer than linkYieldAfter, the
// oldest of the hosts gone longest; nil when fewer than n may, so that none
// is ended for a link that would still find no room. Called with mu held.
func (k *linkKeeper) yielding(addr string, now time.Time, n int) []string {
	type candidate struct {
		id   string
		away time.Duration
		made time.Time
	}
	var cs []candidate
	for id := range k.addrs[addr] {
		kl := k.kept[id]
		if away := k.away(kl.owner, now); away > linkYieldAfter {
			cs = append(cs, candidate{id, away, kl.created})
		}
	}
	if len(cs) < n {
		return nil
	}
	slices.SortFunc(cs, func(a, b candidate) int {
		if c := cmp.Compare(b.away, a.away); c != 0 {
			return c
		}
		if c := a.made.Compare(b.made); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	ids := make([]string, n)
	for i := range ids {
		ids[i] = cs[i].id
	}
	return ids
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
	k := &linkKeeper{files: files, kept: map[string]keptLink{}, addrs: map[string]map[string]struct{}{}, live: map[string]int{}, seen: map[string]time.Time{}, undeleted: map[string]struct{}{}}
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
		k.hold(f.ID, keptLink{hash: f.TokenHash, owner: f.Owner, addr: f.Addr, created: f.CreatedAt})
		k.seen[f.Owner] = now
		restored++
	}
	if restored > 0 {
		s.log.Info("kept links restored", "links", restored)
	}
}

// Why keepLink refuses a link.
var (
	errLinksFull = errors.New("this address holds as many links as this switchyard keeps; revoke one, or ask the operator for a host token")
	errLinkGone  = errors.New("the link was revoked, or its session ended, as it was made")
)

// keepLink makes a link minted for a hosted session of owner durable and
// writes its file, within the bounds. At an open host's address with no
// room left, links whose host has been gone longer than linkYieldAfter
// give their places up (yielding) and are ended, only when that leaves the
// new link room. It refuses (the link dropped) when the bounds leave none,
// ending no other link, and keeps nothing of a link the store no longer
// holds unrevoked: one revoked or dropped since it was made is neither
// counted nor written, nor has any other ended for it.
func (s *Server) keepLink(link *share.Link, token, owner, addr string) error {
	k := s.keeper
	if k == nil || owner == "" {
		return nil
	}
	h := share.HashToken(token)
	now := time.Now()
	k.fmu.Lock()
	k.mu.Lock()
	var (
		err     error
		yielded []string
		ended   []*share.Link
	)
	if s.links.Count() > maxDurableLinks {
		err = errLinksFull
	} else if over := len(k.addrs[addr]) - s.cfg.Switchyard.OpenHostLinks + 1; addr != "" && over > 0 {
		if yielded = k.yielding(addr, now, over); yielded == nil {
			err = errLinksFull
		}
	}
	switch {
	case err != nil:
		s.links.Drop(link.ID)
	// Under mu, so a drop of the link (its file and its count, which
	// follows the store's) comes after this whole or finds nothing.
	case !s.links.SetOwner(link.ID, owner, addr):
		err, yielded = errLinkGone, nil
	default:
		// The links ended for the new one leave the keeper and the store
		// together under mu, so a host coming back (hostUp) either finds
		// them gone or keeps them.
		for _, id := range yielded {
			k.release(id)
			if gone, ok := s.links.Drop(id); ok {
				ended = append(ended, gone)
			}
		}
		k.hold(link.ID, keptLink{hash: hex.EncodeToString(h[:]), owner: owner, addr: addr, created: link.CreatedAt})
	}
	k.mu.Unlock()
	for _, id := range yielded {
		s.deleteFile(id)
		s.log.Info("a kept link gave its place to a new one, its host gone", "link", id)
	}
	if err == nil {
		link.Owner, link.Addr = owner, addr
		s.saveLinkFile(link.ID)
	}
	k.fmu.Unlock()
	for _, l := range ended {
		s.closeLinkViewers(l)
	}
	return err
}

// writeLinkFile writes a kept link's file as the store holds it now.
func (s *Server) writeLinkFile(linkID string) {
	k := s.keeper
	k.fmu.Lock()
	defer k.fmu.Unlock()
	s.saveLinkFile(linkID)
}

// saveLinkFile is writeLinkFile with fmu held: the link as the store holds
// it now, while it is kept and opens anything; nothing for one revoked,
// dropped or no longer kept.
func (s *Server) saveLinkFile(linkID string) {
	k := s.keeper
	k.mu.Lock()
	hash := k.kept[linkID].hash
	k.mu.Unlock()
	if hash == "" {
		return
	}
	link, ok := s.links.Get(linkID)
	if !ok || link.Revoked || link.Owner == "" {
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
			s.writeLinkFile(l.ID)
		}
	}
}

// dropLinkFile forgets a kept link's file and its place in its address's
// count. Calling it again for the same link changes nothing. A save of the
// file that has begun ends before the delete; one after it writes nothing.
func (s *Server) dropLinkFile(linkID string) {
	k := s.keeper
	if k == nil {
		return
	}
	k.fmu.Lock()
	defer k.fmu.Unlock()
	k.mu.Lock()
	k.release(linkID)
	k.mu.Unlock()
	s.deleteFile(linkID)
}

// hostUp notes that a host instance holds a connection, until the function
// it returns is called as the connection ends: while an instance holds one,
// its kept links are neither swept nor give their places up, and its time
// away starts when its last one ends. Called before the host is told which
// of its links the switchyard holds, so a link ended for its place is
// either ended before (and not told) or kept.
func (s *Server) hostUp(owner string) func() {
	k := s.keeper
	if k == nil || owner == "" {
		return func() {}
	}
	k.mu.Lock()
	k.live[owner]++
	k.seen[owner] = time.Now()
	k.mu.Unlock()
	return func() {
		k.mu.Lock()
		if k.live[owner]--; k.live[owner] <= 0 {
			delete(k.live, owner)
		}
		k.seen[owner] = time.Now()
		k.mu.Unlock()
	}
}

// sweepLinks drops the kept links that expired, and those whose host has
// been gone for linkOrphanAfter, closing whoever is attached through them,
// deletes the files that could not be deleted before, and forgets the host
// instances neither connected nor holding a kept link.
func (s *Server) sweepLinks(now time.Time) {
	k := s.keeper
	if k == nil {
		return
	}
	k.fmu.Lock()
	for id := range k.undeleted {
		s.deleteFile(id)
	}
	k.fmu.Unlock()
	// due is whether l goes now. Called with mu held.
	due := func(l *share.Link) bool {
		return k.away(l.Owner, now) > linkOrphanAfter || (l.ExpiresAt != nil && !now.Before(*l.ExpiresAt))
	}
	holding := map[string]bool{}
	for _, l := range s.links.Durable() {
		k.mu.Lock()
		d := due(l)
		k.mu.Unlock()
		if !d || !s.endKept(l.ID, due) {
			holding[l.Owner] = true
		}
	}
	k.mu.Lock()
	// And the links kept since the list above.
	for _, kl := range k.kept {
		holding[kl.owner] = true
	}
	for owner := range k.seen {
		if k.live[owner] == 0 && !holding[owner] {
			delete(k.seen, owner)
		}
	}
	k.mu.Unlock()
}

// endKept ends the kept link id if due still says so, looked at again under
// the keeper's locks (its host may be back meanwhile): it leaves the keeper
// and the store together, its file goes, and whoever is still attached
// through it is closed, as on a revoke, on the sessions it named as it
// went (a run update may have changed them since it was chosen). False
// when it stays.
func (s *Server) endKept(id string, due func(*share.Link) bool) bool {
	k := s.keeper
	k.fmu.Lock()
	k.mu.Lock()
	l, ok := s.links.Get(id)
	if !ok || !due(l) {
		k.mu.Unlock()
		k.fmu.Unlock()
		return !ok
	}
	k.release(id)
	gone, ok := s.links.Drop(id)
	k.mu.Unlock()
	s.deleteFile(id)
	k.fmu.Unlock()
	if ok {
		s.closeLinkViewers(gone)
	}
	return true
}
