package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rocksolidlabs/conductor/internal/proto"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// The cross-session Touched index for the command palette's `:` prefix
// (#304, docs/plans/touched-index.md): every file every listed session's
// agent touched, from the index each session keeps (session.MaxTouched
// files, the least recently touched evicted first), in one admin reply.
const (
	// touchedLimitDefault is the files listed per session when the query
	// names none; touchedLimitMax the most it may ask for, the index's cap.
	touchedLimitDefault = 500
	touchedLimitMax     = session.MaxTouched
	// touchedReplyMax bounds the reply's JSON (about: each session's head
	// and each file's object are counted, the punctuation between them
	// not): the sessions are filled in the list's order, and the one that
	// would pass it is cut short.
	touchedReplyMax = 2 << 20
)

// toucher is a session driver that keeps a touched index: a server session
// (its own, the deny list applied) or a hosted one (fed by the host's reports
// since it connected).
type toucher interface {
	TouchedFiles() []proto.TouchedFile
}

// touchedSession is one session of the reply: who it is, where it runs,
// and its files, the most recently touched first.
type touchedSession struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	AgentID   string              `json:"agentId"`
	Kind      string              `json:"kind"`
	Status    string              `json:"status"`
	Cwd       string              `json:"cwd"`
	Touched   []proto.TouchedFile `json:"touched"`
	Truncated bool                `json:"truncated,omitempty"`
}

type touchedReply struct {
	Sessions []touchedSession `json:"sessions"`
	// Truncated says the reply's size cut a session short, or left sessions
	// out, past the one it marks.
	Truncated bool `json:"truncated,omitempty"`
}

// handleTouched answers GET /api/touched: the listed sessions' touched
// files, the most recently touched first within a session, at most `limit`
// per session (touchedLimitDefault, touchedLimitMax) and about
// touchedReplyMax bytes in all. A session with nothing touched is left out.
// Admin only.
func (s *Server) handleTouched(w http.ResponseWriter, r *http.Request) {
	q, ok := queryValues(w, r)
	if !ok {
		return
	}
	limit := touchedLimitDefault
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return
		}
		limit = min(n, touchedLimitMax)
	}
	writeJSON(w, http.StatusOK, s.touchedIndex(limit, touchedReplyMax))
}

// touchedIndex builds the reply: the sessions in the registry's order (the
// newest first), each with at most limit files (marked truncated past it),
// the whole held to about budget bytes of JSON.
func (s *Server) touchedIndex(limit, budget int) touchedReply {
	reply := touchedReply{Sessions: []touchedSession{}}
	used := 0
	for _, info := range s.registry.List() {
		d, ok := s.registry.Get(info.ID)
		if !ok {
			continue
		}
		t, ok := d.(toucher)
		if !ok {
			continue
		}
		files := t.TouchedFiles()
		if len(files) == 0 {
			continue
		}
		entry := touchedSession{ID: info.ID, Name: info.Name, AgentID: info.AgentID, Kind: string(info.Kind), Status: string(info.Status), Cwd: info.Cwd, Touched: []proto.TouchedFile{}}
		if len(files) > limit {
			files = files[:limit]
			entry.Truncated = true
		}
		head, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		if used+len(head)+1 > budget {
			reply.Truncated = true
			break
		}
		used += len(head) + 1
		for _, f := range files {
			b, err := json.Marshal(f)
			if err != nil {
				continue
			}
			if used+len(b)+1 > budget {
				entry.Truncated = true
				reply.Truncated = true
				break
			}
			used += len(b) + 1
			entry.Touched = append(entry.Touched, f)
		}
		reply.Sessions = append(reply.Sessions, entry)
		if reply.Truncated {
			break
		}
	}
	return reply
}
