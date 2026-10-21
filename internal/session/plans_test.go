package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// plansLocal is a session whose agent keeps its plans under
// home/.claude/plans (home a temporary directory, the process's HOME for
// the test, as ~ expands), with a plan written there, the agent's
// credentials beside the plans directory, and a file of the home's own.
func plansLocal(t *testing.T) (s *Local, home, plans, plan string) {
	t.Helper()
	home = realDir(t, t.TempDir())
	t.Setenv("HOME", home)
	plans = filepath.Join(home, ".claude", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	plan = filepath.Join(plans, "plan-creating-a-file.md")
	os.WriteFile(plan, []byte("# Plan\n\nCreate hello.txt containing hi.\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"token":"secret"}`), 0o600)
	os.WriteFile(filepath.Join(home, "secret.txt"), []byte("mine\n"), 0o600)
	s, _ = newLocalWith(t, Options{ScrollbackBytes: 4096, Plans: plans})
	return s, home, plans, plan
}

// fileReplyBody is fileReply with the frame's body.
func fileReplyBody(t *testing.T, sink *chanSink, reqID string) (proto.FileHeader, []byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		frames := append([][]byte(nil), sink.frames...)
		sink.mu.Unlock()
		for _, fr := range frames {
			f, err := proto.Decode(fr)
			if err != nil || f.Type != proto.TypeFile {
				continue
			}
			h, body, err := proto.DecodeFile(f.Payload)
			if err == nil && h.ReqID == reqID {
				return h, body
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no FILE reply for %s", reqID)
	return proto.FileHeader{}, nil
}

func get(t *testing.T, s *Local, sub *Subscription, sink *chanSink, reqID string, req proto.FileGet) (proto.FileHeader, []byte) {
	t.Helper()
	req.ReqID = reqID
	if err := s.FileGet(sub, req); err != nil {
		t.Fatal(err)
	}
	return fileReplyBody(t, sink, reqID)
}

// A plan the agent wrote outside the folder (its hook's file event names
// it) opens, lists under `plans` and saves through the plans root, by its
// path or through ~; before the agent names it, after the agent deletes
// it, and for every other file of the config directory (the credentials
// beside the plans, another plan the agent did not name, a link out of the
// plans directory and where it leads) the read is refused as any outside
// path is (#289).
func TestPlanOpensAndSavesThroughThePlansRoot(t *testing.T) {
	s, home, plans, plan := plansLocal(t)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	outside := "path is outside the session directory"
	if h, _ := get(t, s, sub, sink, "r0", proto.FileGet{Path: plan}); h.Kind != "error" || h.Error.Code != "denied" || h.Error.Message != outside {
		t.Fatalf("before the agent names it: %+v %+v", h, h.Error)
	}
	if h, _ := get(t, s, sub, sink, "p0", proto.FileGet{Op: proto.FileOpPlans}); h.Kind != "plans" || h.Exists || len(h.Entries) != 0 || h.Path != plans {
		t.Fatalf("plans before any: %+v", h)
	}
	// The agent writes its plan: its hook reports the file (a while ago,
	// so that the save below is its own entry, not coalesced into it).
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: plan, Tool: "Write", ByName: "claude", At: time.Now().UTC().Add(-time.Minute)})
	read, body := get(t, s, sub, sink, "r1", proto.FileGet{Path: plan})
	if read.Kind != "file" || string(body) != "# Plan\n\nCreate hello.txt containing hi.\n" || read.Sha256 == "" || read.ReadOnly {
		t.Fatalf("the plan: %+v %q", read, body)
	}
	if h, _ := get(t, s, sub, sink, "r2", proto.FileGet{Path: "~/.claude/plans/plan-creating-a-file.md", Stat: true}); h.Kind != "file" || !h.Exists || h.Path != plan {
		t.Fatalf("through ~: %+v", h)
	}
	if h, _ := get(t, s, sub, sink, "p1", proto.FileGet{Op: proto.FileOpPlans}); h.Kind != "plans" || !h.Exists || h.Path != plans || len(h.Entries) != 1 || h.Entries[0].Name != "plan-creating-a-file.md" || h.Entries[0].Dir || h.Entries[0].Size != int64(len(body)) {
		t.Fatalf("plans: %+v", h)
	}
	if h, _ := get(t, s, sub, sink, "t1", proto.FileGet{Op: proto.FileOpTouched}); h.Kind != "touched" || len(h.Touched) != 1 || h.Touched[0].Path != plan {
		t.Fatalf("touched: %+v", h)
	}
	// A save goes through the same root, and is recorded by the plan's whole path.
	next := []byte("# Plan\n\nCreate hello.txt containing hi.\n\nThen say so.\n")
	save(s, sub, "w1", plan, next, 512, read.Sha256, false)
	if h := fileReply(t, sink, "w1"); h.Kind != "written" || h.Sha256 != sha256Hex(next) || h.Path != plan {
		t.Fatalf("written: %+v %+v", h, h.Error)
	}
	if b, _ := os.ReadFile(plan); string(b) != string(next) {
		t.Fatal("the plan on disk is not what was saved")
	}
	var saved bool
	for _, e := range s.Activity() {
		if e.Type == ActivityFile && e.Op == FileOpWrite && e.Path == plan && e.Tool == "editor" && e.ByName == "nate" {
			saved = true
		}
	}
	if !saved {
		t.Fatalf("the save is not in Activity by the plan's path: %+v", s.Activity())
	}
	// Refused: the agent's credentials beside the plans, a plan the agent
	// did not name, a link in the plans directory out of it (reported as
	// the agent's), and the file it leads to.
	other := filepath.Join(plans, "another-session.md")
	os.WriteFile(other, []byte("# Another\n"), 0o644)
	link := filepath.Join(plans, "link.md")
	if err := os.Symlink(filepath.Join(home, "secret.txt"), link); err != nil {
		t.Skip("no symlinks here")
	}
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: link, Tool: "Write", ByName: "claude"})
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpRead, Path: filepath.Join(home, ".claude", ".credentials.json"), Tool: "Read", ByName: "claude"})
	for i, p := range []string{filepath.Join(home, ".claude", ".credentials.json"), other, link, filepath.Join(home, "secret.txt"), filepath.Join(home, ".claude"), plans} {
		h, _ := get(t, s, sub, sink, "x"+string(rune('0'+i)), proto.FileGet{Path: p})
		if h.Kind != "error" || h.Error.Code != "denied" || h.Error.Message != outside {
			t.Fatalf("%s: %+v %+v", p, h, h.Error)
		}
	}
	if h, _ := get(t, s, sub, sink, "p2", proto.FileGet{Op: proto.FileOpPlans}); len(h.Entries) != 1 || h.Entries[0].Name != "plan-creating-a-file.md" {
		t.Fatalf("plans lists the agent's own plan alone: %+v", h)
	}
	save(s, sub, "w2", other, []byte("over\n"), 512, "", true)
	if h := fileReply(t, sink, "w2"); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("a save of another plan: %+v", h)
	}
	// Deleted by the agent: forgotten.
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpDelete, Path: plan, Tool: "Bash", ByName: "claude"})
	if h, _ := get(t, s, sub, sink, "r3", proto.FileGet{Path: plan}); h.Kind != "error" || h.Error.Message != outside {
		t.Fatalf("after the delete: %+v", h)
	}
}

// The plan the agent asks approval for (its hook's planFilePath, or the
// path its dialog prints: NotePlan) is learnt as a file event's is, and the
// approval carries it: the hook's report, then the dialog read off the
// screen in its place; a question asked after the approval was answered,
// with no state between (Claude Code's next permission dialog), carries
// none; named while the approval already shows, the plan is set on it and
// told; a path that is not a plan's (the credentials) is neither learnt nor
// carried.
func TestPlanFromTheApprovalRidesTheQuestion(t *testing.T) {
	s, home, _, plan := plansLocal(t)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	s.SetAttentionPlan(AttentionNeedsInput, "Claude Code has a plan for you to approve", SourceAPI, KindPermission, nil, "~/.claude/plans/plan-creating-a-file.md")
	if att := s.Info().Attention; att.State != AttentionNeedsInput || att.Plan != plan {
		t.Fatalf("the question carries the plan: %+v", att)
	}
	if h, _ := get(t, s, sub, sink, "r1", proto.FileGet{Path: plan, Stat: true}); h.Kind != "file" {
		t.Fatalf("learnt from the approval: %+v", h)
	}
	// The dialog read off the screen takes the hook's place: the same plan.
	s.SetAttentionFull(AttentionNeedsInput, "Would you like to proceed?", SourceQuestion, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}})
	if att := s.Info().Attention; att.Source != SourceQuestion || att.Plan != plan {
		t.Fatalf("the dialog carries it too: %+v", att)
	}
	// Answered, and the next dialog drawn straight after (no state between): not about the plan.
	s.SetAttentionFull(AttentionNeedsInput, "Do you want to proceed?\nBash command: ls", SourceQuestion, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}})
	if att := s.Info().Attention; att.Plan != "" {
		t.Fatalf("the next dialog carries none: %+v", att)
	}
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, nil)
	if att := s.Info().Attention; att.Plan != "" {
		t.Fatalf("the next report carries none: %+v", att)
	}
	s.SetAttentionFull(AttentionWorking, "", SourceAPI, "", nil)
	if att := s.Info().Attention; att.Plan != "" {
		t.Fatalf("working carries no plan: %+v", att)
	}
	// The dialog first, the hook's report after it (the screen settled quickly): the report changes nothing of the dialog but
	// its plan, set on it and told.
	s.SetAttentionFull(AttentionNeedsInput, "Would you like to proceed?", SourceQuestion, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}})
	if att := s.Info().Attention; att.Plan != "" {
		t.Fatalf("nothing named yet: %+v", att)
	}
	id := s.Info().Attention.ID
	if err := s.TrySetAttentionPlan(AttentionNeedsInput, "Claude Code has a plan for you to approve", SourceAPI, KindPermission, nil, plan); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.Source != SourceQuestion || att.ID != id || att.Plan != plan {
		t.Fatalf("set on the question showing: %+v", att)
	}
	var told bool
	sink.mu.Lock()
	for _, fr := range sink.frames {
		f, err := proto.Decode(fr)
		if err != nil || f.Type != proto.TypeControl {
			continue
		}
		var m map[string]any
		if json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.CtlAttention && m["plan"] == plan {
			told = true
		}
	}
	sink.mu.Unlock()
	if !told {
		t.Fatal("the plan set on the question showing was not told to the client")
	}
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, nil)
	if att := s.Info().Attention; att.Plan != "" {
		t.Fatalf("named once: %+v", att)
	}
	s.SetAttentionFull(AttentionDone, "", SourceAPI, KindDone, nil)
	s.SetAttentionPlan(AttentionNeedsInput, "Claude Code has a plan for you to approve", SourceAPI, KindPermission, nil, filepath.Join(home, ".claude", ".credentials.json"))
	if att := s.Info().Attention; att.State != AttentionNeedsInput || att.Plan != "" {
		t.Fatalf("not a plan: %+v", att)
	}
	// A state that is no question names nothing either.
	s.SetAttentionPlan(AttentionWorking, "", SourceAPI, "", nil, plan)
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpDelete, Path: plan, Tool: "Bash"})
	s.SetAttentionPlan(AttentionDone, "", SourceAPI, KindDone, nil, plan)
	if h, _ := get(t, s, sub, sink, "r3", proto.FileGet{Path: plan}); h.Kind != "error" {
		t.Fatalf("a working or done report names no plan: %+v", h)
	}
	if h, _ := get(t, s, sub, sink, "r2", proto.FileGet{Path: filepath.Join(home, ".claude", ".credentials.json")}); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("the credentials: %+v", h)
	}
}

// A plans directory named through a link (Options.Plans a link to it, as
// /var is to /private/var on macOS): a plan the agent names by the
// directory's target, or through the link, is the same plan, learnt,
// listed once, read by either spelling and forgotten by either.
func TestPlansDirectoryThroughALink(t *testing.T) {
	home := realDir(t, t.TempDir())
	target := filepath.Join(home, "real-plans")
	link := filepath.Join(home, ".claude", "plans")
	os.MkdirAll(target, 0o755)
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here")
	}
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, Plans: link})
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(target, "a.md"), []byte("# A\n"), 0o644)
	os.WriteFile(filepath.Join(target, "b.md"), []byte("# B\n"), 0o644)
	// Named by the target, and through the link.
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: filepath.Join(target, "a.md"), Tool: "Write"})
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: filepath.Join(link, "b.md"), Tool: "Write"})
	for i, p := range []string{filepath.Join(target, "a.md"), filepath.Join(link, "a.md"), filepath.Join(target, "b.md"), filepath.Join(link, "b.md")} {
		if h, _ := get(t, s, sub, sink, "r"+string(rune('0'+i)), proto.FileGet{Path: p}); h.Kind != "file" {
			t.Fatalf("%s: %+v %+v", p, h, h.Error)
		}
	}
	if h, _ := get(t, s, sub, sink, "p1", proto.FileGet{Op: proto.FileOpPlans}); len(h.Entries) != 2 || h.Entries[0].Name != "a.md" || h.Entries[1].Name != "b.md" {
		t.Fatalf("listed once each: %+v", h)
	}
	// Forgotten by the other spelling.
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpDelete, Path: filepath.Join(link, "a.md"), Tool: "Bash"})
	if h, _ := get(t, s, sub, sink, "r5", proto.FileGet{Path: filepath.Join(target, "a.md")}); h.Kind != "error" {
		t.Fatalf("forgotten: %+v", h)
	}
}

// The hook's report of the approval, with its plan, takes the place of a
// bell's wait: its question carries the plan, the bell's wait is not
// touched, and the dialog read off the screen in its place keeps it; the
// dialog's footer, read on a later look of the same dialog, sets the plan
// on it; a report the bucket refuses names nothing.
func TestPlanRidesTheReportThatTakesABellsPlace(t *testing.T) {
	s, _, _, plan := plansLocal(t)
	s.SetAttentionFull(AttentionNeedsInput, "terminal bell", SourceBell, "", nil)
	if err := s.TrySetAttentionPlan(AttentionNeedsInput, "Claude Code has a plan for you to approve", SourceAPI, KindPermission, nil, plan); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.Source != SourceAPI || att.Plan != plan {
		t.Fatalf("the report's question: %+v", att)
	}
	s.SetAttentionFull(AttentionNeedsInput, "Would you like to proceed?", SourceQuestion, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}})
	if att := s.Info().Attention; att.Source != SourceQuestion || att.Plan != plan {
		t.Fatalf("kept by the dialog: %+v", att)
	}
	// The same dialog read again, its footer now whole: the plan set on it (none was, say, at a narrower width).
	s.SetAttentionFull(AttentionWorking, "", SourceAPI, "", nil)
	s.SetAttentionFull(AttentionNeedsInput, "Would you like to proceed?", SourceQuestion, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}})
	id := s.Info().Attention.ID
	s.SetAttentionPlan(AttentionNeedsInput, "Would you like to proceed?", SourceQuestion, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}}, plan)
	if att := s.Info().Attention; att.ID != id || att.Plan != plan {
		t.Fatalf("the footer read later: %+v", att)
	}
	// The bucket empty: the report is refused whole, its plan with it.
	s.SetAttentionFull(AttentionWorking, "", SourceAPI, "", nil)
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpDelete, Path: plan, Tool: "Bash"})
	for s.TrySetAttentionFull(AttentionWorking, "", SourceAPI, "", nil) == nil {
	}
	if err := s.TrySetAttentionPlan(AttentionNeedsInput, "Claude Code has a plan for you to approve", SourceAPI, KindPermission, nil, plan); err != ErrRateLimited {
		t.Fatalf("refused: %v", err)
	}
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := get(t, s, sub, sink, "r1", proto.FileGet{Path: plan}); h.Kind != "error" {
		t.Fatalf("a refused report names nothing: %+v", h)
	}
}

// A request's path is allowed by what it resolves to, the file then opened
// from the plans folder with no link followed: a link to a plan reads the
// plan; the same link made to lead to another file of the directory, one
// the agent did not name, is refused, however the request spells it.
func TestPlanAllowedByWhatThePathResolvesTo(t *testing.T) {
	s, home, plans, plan := plansLocal(t)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: plan, Tool: "Write", ByName: "claude"})
	other := filepath.Join(plans, "another-session.md")
	os.WriteFile(other, []byte("# Another\n"), 0o644)
	alias := filepath.Join(home, "alias.md")
	if err := os.Symlink(plan, alias); err != nil {
		t.Skip("no symlinks here")
	}
	if h, body := get(t, s, sub, sink, "r1", proto.FileGet{Path: alias}); h.Kind != "file" || h.Path != plan || len(body) == 0 {
		t.Fatalf("through a link: %+v", h)
	}
	os.Remove(alias)
	os.Symlink(other, alias)
	if h, _ := get(t, s, sub, sink, "r2", proto.FileGet{Path: alias}); h.Kind != "error" || h.Error.Code != "denied" || h.Error.Message != errOutside.Error() {
		t.Fatalf("the link made to lead elsewhere: %+v %+v", h, h.Error)
	}
	save(s, sub, "w1", alias, []byte("over\n"), 512, "", true)
	if h := fileReply(t, sink, "w1"); h.Kind != "error" || h.Error.Code != "denied" {
		t.Fatalf("a save through it: %+v", h)
	}
	if b, _ := os.ReadFile(other); string(b) != "# Another\n" {
		t.Fatal("the other plan was written")
	}
}

// A plans directory that is not there when the session starts (Claude
// Code makes it with the first plan) is opened once the agent reports a
// file in it.
func TestPlansDirectoryMadeLater(t *testing.T) {
	home := realDir(t, t.TempDir())
	plans := filepath.Join(home, ".claude", "plans")
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, Plans: plans})
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nate"}, sink)
	if err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(plans, "plan.md")
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: plan, Tool: "Write"})
	if h, _ := get(t, s, sub, sink, "r1", proto.FileGet{Path: plan}); h.Kind != "error" {
		t.Fatalf("nothing there yet: %+v", h)
	}
	os.MkdirAll(plans, 0o755)
	os.WriteFile(plan, []byte("# Plan\n"), 0o644)
	s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: plan, Tool: "Write"})
	if h, body := get(t, s, sub, sink, "r2", proto.FileGet{Path: plan}); h.Kind != "file" || string(body) != "# Plan\n" {
		t.Fatalf("once written: %+v", h)
	}
}

// The plan's path on the last row of Claude Code's plan approval, the rows
// a narrow screen wrapped it over joined; nothing from another footer.
func TestPlanFooter(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"   ❯ 1. Yes, and use auto mode\n     2. Yes, manually approve edits\n\n   ctrl+g to edit in Vim · ~/.claude/plans/plan.md", "~/.claude/plans/plan.md"},
		{"   ❯ 1. Yes\n   ctrl+g to edit in Vim · ~/.claude/pla\n   ns/plan.md\n", "~/.claude/plans/plan.md"},
		{"   ❯ 1. Yes\n     2. No\n\n   Esc to cancel · Tab to amend", ""},
		{"ctrl+g to edit in Vim · /tmp/x.md\n\n\n\n\n\n\n   the prompt", ""},
	} {
		if got := planFooter(c.text); got != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}
