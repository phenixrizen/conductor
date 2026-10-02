package session

import (
	"regexp"
	"strings"
	"testing"
)

// The agent's reported id becomes the session's, by the agent's policy: the
// latest one, or the lowest (Codex's main thread, not its title thread); a
// turn makes it resumable.
func TestReportAgentSession(t *testing.T) {
	s, _ := newLocalWith(t, quiet(Options{}))
	s.ReportAgentSession("01a0fc76-4817", PolicyLowest, false)
	s.ReportAgentSession("01a0fc76-3ab9", PolicyLowest, true)
	s.ReportAgentSession("01a0fc76-9999", PolicyLowest, true)
	if as := s.Info().AgentSession; as == nil || as.ID != "01a0fc76-3ab9" || !as.Resumable || as.Source != AgentSessionHook {
		t.Fatalf("lowest: %+v", as)
	}
	s, _ = newLocalWith(t, quiet(Options{}))
	s.SetAgentSession(AgentSession{ID: "a", Source: AgentSessionSet})
	s.ReportAgentSession("a", PolicyLatest, false)
	if as := s.Info().AgentSession; as.ID != "a" || as.Resumable || as.Source != AgentSessionSet {
		t.Fatalf("set: %+v", as)
	}
	s.ReportAgentSession("b", PolicyLatest, true)
	if as := s.Info().AgentSession; as.ID != "b" || !as.Resumable {
		t.Fatalf("latest: %+v", as)
	}
	re := regexp.MustCompile(`^[a-z0-9-]+$`)
	for id, want := range map[string]bool{"abc-1": true, "-abc": false, "": false, strings.Repeat("a", 129): false, "a b": false} {
		if got := ValidAgentSessionID(id, re); got != want {
			t.Errorf("ValidAgentSessionID(%q) = %v", id, got)
		}
	}
}
