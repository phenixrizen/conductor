package session

import (
	"strings"
	"unicode"

	"github.com/rocksolidlabs/conductor/internal/proto"
)

// CleanSessionName holds a name a person gives a session (a rename, PATCH
// /api/sessions/{id}) to what its listings show: its control characters
// dropped (C0, DEL and C1), the space around it trimmed, and cut to
// proto.MaxSessionName bytes at a character boundary. "" when nothing is
// left, which a rename refuses.
func CleanSessionName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	return cutBytes(s, proto.MaxSessionName)
}

// Rename gives the session name, cleaned already (CleanSessionName): its
// Info's from now on, and the one a resume of it starts under (Launched). A
// name that changes notifies OnChange, which takes it to the listings and
// the events stream.
func (s *Local) Rename(name string) {
	s.mu.Lock()
	changed := s.info.Name != name
	s.info.Name = name
	s.renamed = true
	s.mu.Unlock()
	if changed {
		s.notifyChange()
	}
}
