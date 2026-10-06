package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

func TestCleanChatText(t *testing.T) {
	for in, want := range map[string]string{
		"  hello  ":                 "hello",
		"a\r\nb":                    "a\nb",
		"a\rb\x07c\x1b[31md":        "abc[31md",
		"tab\tkept":                 "tab\tkept",
		"\xff\xfe":                  "\ufffd",
		"<b>&amp;</b>":              "<b>&amp;</b>",
		"\n\n":                      "",
		"line\u2028break":           "line\u2028break",
		"zero\x00width":             "zerowidth",
		"end with a newline\n":      "end with a newline",
		"  keep inner\n\nblanks  ":  "keep inner\n\nblanks",
		"emoji 🚂 stays":             "emoji 🚂 stays",
		"delete\x7fchar":            "deletechar",
		"nbsp\u00a0stays":           "nbsp\u00a0stays",
		"combined\u0301 accents":    "combined\u0301 accents",
		"windows\r\nlines\r\n":      "windows\nlines",
		"\t\tindented":              "indented",
		"a\x1b]0;title\x07b":        "a]0;titleb",
		"ok":                        "ok",
		strings.Repeat("x", 3000):   strings.Repeat("x", 3000),
		"form\x0cfeed":              "formfeed",
		"vertical\x0btab":           "verticaltab",
		"escape\x1bonly":            "escapeonly",
		"mixed \r\n\t ok":           "mixed \n\t ok",
		"trailing tab\t":            "trailing tab",
		"the agent said: \"yes\"":   "the agent said: \"yes\"",
		"back\\slash":               "back\\slash",
		"emoji\u200dzwj":            "emoji\u200dzwj",
		"rtl\u202eoverride":         "rtl\u202eoverride",
		"soft\u00adhyphen":          "soft\u00adhyphen",
		"\u0085next line":           "next line",
		"c1\u009bcontrol":           "c1control",
		"bom\ufeffstays":            "bom\ufeffstays",
		"中文 也 可以":                   "中文 也 可以",
		"a\n\r\nb":                  "a\n\nb",
		"last\r":                    "last",
		" \t \n ":                   "",
		"keep  double  spaces":      "keep  double  spaces",
		"url https://x.example/a?b": "url https://x.example/a?b",
	} {
		if got := CleanChatText(in); got != want {
			t.Errorf("CleanChatText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatRingKeepsTheLastMaxChat(t *testing.T) {
	var r chatRing
	for i := range MaxChat + 7 {
		r.add(ChatMessage{ID: fmt.Sprint(i), Text: fmt.Sprint(i)})
	}
	snap := r.snapshot()
	if len(snap) != MaxChat || snap[0].ID != "7" || snap[len(snap)-1].ID != fmt.Sprint(MaxChat+6) {
		t.Fatalf("ring: %d kept, first %s, last %s", len(snap), snap[0].ID, snap[len(snap)-1].ID)
	}
	if _, ok := r.find("3"); ok {
		t.Fatal("a dropped message was found")
	}
	if m, ok := r.find("100"); !ok || m.Text != "100" {
		t.Fatalf("find: %v %+v", ok, m)
	}
	snap[0].Text = "changed"
	if r.buf[0].Text == "changed" {
		t.Fatal("the snapshot shares the ring's memory")
	}
}

func TestChatBucketBoundsOneConnectionsPosts(t *testing.T) {
	var b chatBucket
	now := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	for i := range proto.ChatBurst {
		if !b.take(now) {
			t.Fatalf("post %d refused within the burst", i)
		}
	}
	if b.take(now) {
		t.Fatal("the burst was not the bound")
	}
	if b.take(now.Add(-time.Second)) {
		t.Fatal("a clock stepping back earned a post")
	}
	n := 0
	for b.take(now.Add(time.Second)) {
		n++
	}
	if n != proto.ChatRatePerSecond {
		t.Fatalf("a second earned %d posts, want %d", n, proto.ChatRatePerSecond)
	}
	if n = 0; true {
		for b.take(now.Add(time.Hour)) {
			n++
		}
	}
	if n != proto.ChatBurst {
		t.Fatalf("an hour earned %d posts, want the burst %d", n, proto.ChatBurst)
	}
}

func chatOf(kind string) func(m map[string]any) bool {
	return func(m map[string]any) bool { return m["t"] == proto.CtlChat && m["kind"] == kind }
}

func TestChatReachesEveryViewerWhateverTheirRole(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	subA, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, a)
	if err != nil {
		t.Fatal(err)
	}
	subB, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "Priya", Cols: 80, Rows: 24}, b)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Chat(subA, proto.ChatPost{T: proto.CtlChat, Text: "  hello <there>\r\n", Nonce: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "hello <there>" || m.Kind != proto.ChatKindMessage || m.Scope != proto.ChatScopeSession || m.By.Name != "Nate" || m.By.Role != RoleControl || m.By.ID != subA.ID || m.Nonce != "n1" || m.ID == "" || m.At.IsZero() {
		t.Fatalf("kept %+v", m)
	}
	got := waitControl(b, chatOf(proto.ChatKindMessage))
	if got == nil || got["text"] != "hello <there>" || got["id"] != m.ID || got["nonce"] != "n1" || got["by"].(map[string]any)["role"] != "control" || got["by"].(map[string]any)["name"] != "Nate" {
		t.Fatalf("the view-only viewer got %v", got)
	}
	if own := waitControl(a, chatOf(proto.ChatKindMessage)); own == nil || own["id"] != m.ID {
		t.Fatalf("the sender's own copy %v", own)
	}
	// A view link may talk; it may not reach the agent.
	if _, err := s.Chat(subB, proto.ChatPost{T: proto.CtlChat, Text: "hi back"}); err != nil {
		t.Fatalf("a view role's post: %v", err)
	}
	if got := waitControl(a, func(x map[string]any) bool { return chatOf(proto.ChatKindMessage)(x) && x["text"] == "hi back" }); got == nil || got["by"].(map[string]any)["role"] != "view" {
		t.Fatalf("the controller got %v", got)
	}
	if _, err := s.Chat(subB, proto.ChatPost{T: proto.CtlChat, Text: "x", To: proto.ChatToAgent}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a view role to the agent: %v", err)
	}
	for _, bad := range []struct {
		post proto.ChatPost
		want error
	}{
		{proto.ChatPost{Text: "  \n "}, ErrChatEmpty},
		{proto.ChatPost{Text: strings.Repeat("y", proto.MaxChatText+1)}, ErrChatTooLong},
		{proto.ChatPost{Text: "x", Scope: "run"}, ErrChatBadScope},
		{proto.ChatPost{Text: "x", To: "core"}, ErrChatBadScope},
	} {
		if _, err := s.Chat(subA, bad.post); !errors.Is(err, bad.want) {
			t.Fatalf("%+v: %v, want %v", bad.post, err, bad.want)
		}
	}
	if _, err := s.Chat(subA, proto.ChatPost{Text: strings.Repeat("z", proto.MaxChatText)}); err != nil {
		t.Fatalf("a message of exactly the bound: %v", err)
	}
}

func TestChatReplayComesAfterTheActivityReplayAndBeforeLiveMessages(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a := newChanSink(false)
	subA, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := s.Chat(subA, proto.ChatPost{Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	c := newChanSink(false)
	subC, err := s.AttachWith(AttachOptions{Role: RoleView, Name: "Jane", Cols: 80, Rows: 24}, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Chat(subA, proto.ChatPost{Text: "live"}); err != nil {
		t.Fatal(err)
	}
	live := waitControl(c, func(m map[string]any) bool { return chatOf(proto.ChatKindMessage)(m) && m["text"] == "live" })
	if live == nil {
		t.Fatal("no live message")
	}
	// In the new viewer's frames: ready, then the history, then the live message; the history holds the three and Nate's join.
	order := map[string]int{}
	var history map[string]any
	for i := 0; i < c.count(); i++ {
		f, err := proto.Decode(c.frame(i))
		if err != nil || f.Type != proto.TypeControl {
			continue
		}
		m := decodeControl(t, c.frame(i))
		switch {
		case m["t"] == proto.CtlReady:
			order["ready"] = i
		case m["t"] == proto.CtlChatHistory:
			order["history"] = i
			history = m
		case chatOf(proto.ChatKindMessage)(m) && m["text"] == "live":
			order["live"] = i
		}
	}
	if history == nil || !(order["ready"] < order["history"] && order["history"] < order["live"]) {
		t.Fatalf("order %v", order)
	}
	var texts, joins []string
	for _, raw := range history["messages"].([]any) {
		m := raw.(map[string]any)
		if m["kind"] == proto.ChatKindMessage {
			texts = append(texts, m["text"].(string))
		}
		if m["kind"] == proto.ChatKindSystem {
			joins = append(joins, m["by"].(map[string]any)["name"].(string)+":"+m["event"].(string))
		}
	}
	if strings.Join(texts, ",") != "one,two,three" || strings.Join(joins, ",") != "Nate:join" || history["more"] != nil || history["scope"] != proto.ChatScopeSession {
		t.Fatalf("history %v", history)
	}
	// The new viewer's own join line reached the earlier viewer, not its own history.
	if got := waitControl(a, func(m map[string]any) bool {
		return chatOf(proto.ChatKindSystem)(m) && m["event"] == "join" && m["by"].(map[string]any)["name"] == "Jane"
	}); got == nil {
		t.Fatal("no join line for Jane")
	}
	_ = subC
}

func TestChatReplayFramesFitTheControlFrame(t *testing.T) {
	var msgs []ChatMessage
	for i := range MaxChat {
		msgs = append(msgs, ChatMessage{ID: fmt.Sprintf("%016d", i), At: time.Now(), Scope: proto.ChatScopeSession, Kind: proto.ChatKindMessage, By: ChatBy{ID: "x", Name: strings.Repeat("N", 40), Role: RoleView}, Text: strings.Repeat(`"`, proto.MaxChatText)})
	}
	frames := chatReplayFrames(proto.ChatScopeSession, msgs)
	if len(frames) < 2 {
		t.Fatalf("%d frames for %d full messages", len(frames), MaxChat)
	}
	total, ids := 0, map[string]bool{}
	for i, f := range frames {
		if len(f) > proto.MaxControl {
			t.Fatalf("frame %d is %d bytes", i, len(f))
		}
		m := decodeControl(t, f)
		more := m["more"] == true
		if more != (i < len(frames)-1) {
			t.Fatalf("frame %d more=%v", i, more)
		}
		for _, raw := range m["messages"].([]any) {
			id := raw.(map[string]any)["id"].(string)
			if ids[id] {
				t.Fatalf("message %s twice", id)
			}
			ids[id] = true
			total++
		}
	}
	// The newest that fit the replay budget, the oldest first across the frames.
	if total == 0 || total > MaxChat || !ids[msgs[len(msgs)-1].ID] {
		t.Fatalf("%d messages replayed, newest included: %v", total, ids[msgs[len(msgs)-1].ID])
	}
	first := decodeControl(t, frames[0])["messages"].([]any)[0].(map[string]any)["id"].(string)
	if first != msgs[len(msgs)-total].ID {
		t.Fatalf("the replay starts at %s, want %s", first, msgs[len(msgs)-total].ID)
	}
	if chatReplayFrames(proto.ChatScopeSession, nil) != nil {
		t.Fatal("frames for no messages")
	}
}

func TestChatSendTypesTheMessageAndMarksIt(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	subA, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, a)
	subB, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "Priya", Cols: 80, Rows: 24}, b)
	m, err := s.Chat(subA, proto.ChatPost{Text: "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	typed := make(chan []byte, 1)
	go func() {
		var acc []byte
		deadline := time.After(3 * time.Second)
		for {
			select {
			case in := <-p.input:
				acc = append(acc, in...)
				if bytes.Contains(acc, []byte("ship it")) && bytes.HasSuffix(acc, []byte("\r")) {
					typed <- acc
					return
				}
			case <-deadline:
				typed <- acc
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.ChatSend(ctx, subA, proto.ChatSend{T: proto.CtlChatSend, Ref: m.ID}); err != nil {
		t.Fatal(err)
	}
	if acc := <-typed; !bytes.Contains(acc, []byte("ship it")) || !bytes.HasSuffix(acc, []byte("\r")) {
		t.Fatalf("typed %q", acc)
	}
	marker := waitControl(b, chatOf(proto.ChatKindSentToAgent))
	if marker == nil || marker["ref"] != m.ID || marker["by"].(map[string]any)["name"] != "Nate" || marker["text"] != nil {
		t.Fatalf("marker %v", marker)
	}
	// Only a controller; only a kept message; never a marker.
	if err := s.ChatSend(ctx, subB, proto.ChatSend{Ref: m.ID}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("view: %v", err)
	}
	if err := s.ChatSend(ctx, subA, proto.ChatSend{Ref: "nope"}); !errors.Is(err, ErrChatUnknownRef) {
		t.Fatalf("unknown: %v", err)
	}
	if err := s.ChatSend(ctx, subA, proto.ChatSend{Ref: marker["id"].(string)}); !errors.Is(err, ErrChatUnknownRef) {
		t.Fatalf("a marker: %v", err)
	}
	if err := s.ChatSend(ctx, subA, proto.ChatSend{Ref: m.ID, Scope: "run"}); !errors.Is(err, ErrChatBadScope) {
		t.Fatalf("run scope: %v", err)
	}
	if history := s.ChatHistory(); len(history) < 4 || history[len(history)-1].Kind != proto.ChatKindSentToAgent {
		t.Fatalf("history %+v", history)
	}
}

func TestChatOnAnEndedSessionIsRefused(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a := newChanSink(false)
	subA, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, a)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Chat(subA, proto.ChatPost{Text: "anyone?"}); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("ended: %v", err)
	}
}

func TestChatJoinAndLeaveLinesCoalesceByName(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a, a2, b := newChanSink(false), newChanSink(false), newChanSink(false)
	subA, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, a)
	subA2, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "Nate", Cols: 80, Rows: 24}, a2)
	subB, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "Priya", Cols: 80, Rows: 24}, b)
	lines := func() []string {
		var out []string
		for _, m := range s.ChatHistory() {
			if m.Kind == proto.ChatKindSystem {
				out = append(out, m.By.Name+":"+m.Event)
			}
		}
		return out
	}
	if got := strings.Join(lines(), ","); got != "Nate:join,Priya:join" {
		t.Fatalf("after the joins: %s", got)
	}
	s.Detach(subA)
	if got := strings.Join(lines(), ","); got != "Nate:join,Priya:join" {
		t.Fatalf("after one of Nate's two left: %s", got)
	}
	s.Detach(subA2)
	s.Detach(subB)
	if got := strings.Join(lines(), ","); got != "Nate:join,Priya:join,Nate:leave,Priya:leave" {
		t.Fatalf("after everyone left: %s", got)
	}
	// Hooks heard each line once, with the session's id.
	var heard []string
	s2, _ := newLocalWith(t, Options{OnChat: func(id string, m ChatMessage) { heard = append(heard, m.Kind+":"+m.Event+":"+m.Text) }})
	subC, _ := s2.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, newChanSink(false))
	s2.Chat(subC, proto.ChatPost{Text: "hi"})
	s2.Detach(subC)
	if got := strings.Join(heard, ","); got != "system:join:,message::hi,system:leave:" {
		t.Fatalf("hook heard %s", got)
	}
}

// The chat frames carry the message untouched by HTML escaping, so a
// message of the bound fits the control frame.
func TestChatFrameOfTheBoundFits(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a := newChanSink(false)
	subA, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Nate", Cols: 80, Rows: 24}, a)
	if _, err := s.Chat(subA, proto.ChatPost{Text: strings.Repeat("<", proto.MaxChatText)}); err != nil {
		t.Fatal(err)
	}
	got := waitControl(a, chatOf(proto.ChatKindMessage))
	if got == nil || len(got["text"].(string)) != proto.MaxChatText {
		t.Fatalf("got %v", got)
	}
}
