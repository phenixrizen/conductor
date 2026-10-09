package session

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/gitcli"
)

// Files seen by git (round 13, G2a): what an agent changed in its working
// tree without a hook naming the file (a shell command's redirect, a
// generator, an agent whose hooks report no paths) still reaches Touched.
// The session takes a snapshot of `git status` when it starts; after each
// tool call and each attention change (the end of a turn, a question) it
// looks again, a moment later, and records a `file` entry, tool `git`, for
// each file whose status, size or time moved since the last look and that no
// file entry named meanwhile. Reads cannot be seen this way. A working
// directory outside a git work tree is not watched.
const (
	gitSeenDelay   = 1500 * time.Millisecond // a burst of tool calls is one look
	gitSeenMax     = 32                      // file entries per look
	gitSeenTimeout = 5 * time.Second
	// GitSeenTool is the tool named on an entry git saw.
	GitSeenTool = "git"
)

type fileSig struct {
	status string
	size   int64
	mod    time.Time
	exists bool
}

type gitSeen struct {
	mu       sync.Mutex
	off      bool // not a work tree, or the session ended
	base     map[string]fileSig
	reported map[string]bool // absolute paths a file entry named since the last look
	timer    *time.Timer
	busy     bool
	again    bool
}

// gitSeenStart takes the first snapshot, in the background.
func (s *Local) gitSeenStart() {
	go s.gitLook()
}

// gitSeenReported notes a file a file entry named (not one git saw), so the
// next look does not add it twice.
func (s *Local) gitSeenReported(e ActivityEntry) {
	if !s.opts.WatchGit || e.Tool == GitSeenTool || e.Path == "" {
		return
	}
	g := &s.gitSeen
	g.mu.Lock()
	if g.reported == nil {
		g.reported = map[string]bool{}
	}
	g.reported[touchedAbs(s.info.Cwd, e.Path)] = true
	g.mu.Unlock()
}

// gitSeenPoke asks for a look gitSeenDelay from now (a later poke moves it).
func (s *Local) gitSeenPoke() {
	if !s.opts.WatchGit {
		return
	}
	g := &s.gitSeen
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.off {
		return
	}
	if g.timer != nil {
		g.timer.Stop()
	}
	g.timer = time.AfterFunc(gitSeenDelay, s.gitLook)
}

// gitLook takes a snapshot and, past the first, records what moved. One look
// at a time; a poke during one runs another after it.
func (s *Local) gitLook() {
	g := &s.gitSeen
	g.mu.Lock()
	if g.off {
		g.mu.Unlock()
		return
	}
	if g.busy {
		g.again = true
		g.mu.Unlock()
		return
	}
	g.busy = true
	g.mu.Unlock()
	for {
		s.gitLookOnce()
		g.mu.Lock()
		if !g.again || g.off {
			g.busy = false
			g.mu.Unlock()
			return
		}
		g.again = false
		g.mu.Unlock()
	}
}

func (s *Local) gitLookOnce() {
	g := &s.gitSeen
	s.mu.Lock()
	cwd, ended := s.info.Cwd, s.info.Status.Ended()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), gitSeenTimeout)
	defer cancel()
	snap, ok := gitSnapshot(ctx, cwd)
	g.mu.Lock()
	if !ok {
		// Not a work tree (or git is missing): nothing to watch, ever.
		if g.base == nil {
			g.off = true
		}
		g.mu.Unlock()
		return
	}
	if ended {
		g.off = true
	}
	if g.base == nil {
		g.base, g.reported = snap, nil
		g.mu.Unlock()
		return
	}
	moved := gitSeenDiff(g.base, snap, g.reported)
	g.base, g.reported = snap, nil
	g.mu.Unlock()
	for _, m := range moved {
		path := m.path
		if r, err := filepath.Rel(cwd, path); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			path = r
		}
		s.Record(ActivityEntry{Type: ActivityFile, Op: m.op, Path: path, Tool: GitSeenTool, ByName: "agent"})
	}
}

// gitSnapshot is the working tree's changed and untracked files, by
// absolute path, each with its status and its size and time on disk.
func gitSnapshot(ctx context.Context, dir string) (map[string]fileSig, bool) {
	if dir == "" {
		return nil, false
	}
	top, changes, _, err := gitcli.Porcelain(ctx, dir)
	if err != nil {
		return nil, false
	}
	snap := make(map[string]fileSig, len(changes))
	for _, c := range changes {
		abs := filepath.Join(top, filepath.FromSlash(c.Path))
		sig := fileSig{status: c.Status}
		if fi, err := os.Lstat(abs); err == nil {
			sig.size, sig.mod, sig.exists = fi.Size(), fi.ModTime(), true
		}
		snap[abs] = sig
	}
	return snap, true
}

type gitMove struct{ path, op string }

// gitSeenDiff is what moved between two snapshots, skipping the files a
// file entry named meanwhile: a file new to the list or changed in status,
// size or time is written (untracked or added), deleted (gone) or edited;
// one that left the list is deleted when it is gone, edited when its time
// moved (put back to HEAD), and nothing when only committed. Sorted by
// path, at most gitSeenMax.
func gitSeenDiff(base, snap map[string]fileSig, reported map[string]bool) []gitMove {
	var out []gitMove
	for p, now := range snap {
		if reported[p] {
			continue
		}
		was, had := base[p]
		if had && was == now {
			continue
		}
		op := FileOpEdit
		switch {
		case !now.exists:
			op = FileOpDelete
		case !had && (now.status == "?" || now.status == "A"):
			op = FileOpWrite
		}
		if had && !was.exists && !now.exists {
			continue
		}
		out = append(out, gitMove{p, op})
	}
	for p, was := range base {
		if _, still := snap[p]; still || reported[p] {
			continue
		}
		fi, err := os.Lstat(p)
		switch {
		case err != nil && was.exists:
			out = append(out, gitMove{p, FileOpDelete})
		case err == nil && !fi.ModTime().Equal(was.mod):
			out = append(out, gitMove{p, FileOpEdit})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	if len(out) > gitSeenMax {
		out = out[:gitSeenMax]
	}
	return out
}
