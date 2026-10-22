package signal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// nextOwn is the next message queued for the host within a second.
func nextOwn(t *testing.T, conn *HostConn) proto.HostAttentionMsg {
	t.Helper()
	select {
	case out := <-conn.Own:
		var m proto.HostAttentionMsg
		if err := json.Unmarshal(out.Text, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(time.Second):
		t.Fatal("nothing queued for the host")
		return proto.HostAttentionMsg{}
	}
}

// The plan a report names goes to the host with that question, and no
// other; the host tells its attention back with the plan, which the
// server's copy then carries; a state that is no question carries none,
// and a plan past the bound is left (#289).
func TestPlanRidesTheQuestionForwardedToTheHost(t *testing.T) {
	hs, conn := register(t, NewHub(session.NewRegistry(4), nil))
	plan := "/home/dev/.claude/plans/plan-creating-a-file.md"
	if err := hs.SetAttentionPlan(session.AttentionNeedsInput, "Claude Code has a plan for you to approve", session.SourceAPI, session.KindPermission, nil, true, plan); err != nil {
		t.Fatal(err)
	}
	if m := nextOwn(t, conn); m.T != proto.HostAttention || m.State != "needs_input" || m.Plan != plan {
		t.Fatalf("forwarded: %+v", m)
	}
	if hs.Info().Attention.Plan != plan {
		t.Fatalf("the server's copy: %+v", hs.Info().Attention)
	}
	// The host applies it and tells its attention back, the dialog read off its screen, with the plan.
	hs.HostAttention(session.AttentionNeedsInput, "Would you like to proceed?", session.SourceQuestion, session.KindPermission, []session.Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}}, "0123456789abcdef", false, "", plan)
	if att := hs.Info().Attention; att.Source != session.SourceQuestion || att.Plan != plan || att.ID != "0123456789abcdef" {
		t.Fatalf("the host's: %+v", att)
	}
	// The next question forwarded carries none unless its report names one.
	if err := hs.SetAttentionFull(session.AttentionNeedsInput, "Allow Bash?", session.SourceAPI, session.KindPermission, nil, true); err != nil {
		t.Fatal(err)
	}
	if m := nextOwn(t, conn); m.Plan != "" {
		t.Fatalf("its own report names none: %+v", m)
	}
	if err := hs.SetAttentionPlan(session.AttentionWorking, "", session.SourceAPI, "", nil, true, plan); err != nil {
		t.Fatal(err)
	}
	if m := nextOwn(t, conn); m.Plan != "" || hs.Info().Attention.Plan != "" {
		t.Fatalf("working carries none: %+v", m)
	}
	hs.HostAttention(session.AttentionNeedsInput, "Allow?", session.SourceQuestion, session.KindPermission, nil, "fedcba9876543210", false, "", "/"+strings.Repeat("q", proto.MaxPlanPath)+".md")
	if att := hs.Info().Attention; att.Plan != "" {
		t.Fatalf("past the bound: %+v", att)
	}
}
