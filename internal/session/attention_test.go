package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/proto"
)

func TestScannerEvents(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   []Event
	}{
		{"bare bell", []string{"hello\a world"}, []Event{{Source: SourceBell}}},
		{"two bells", []string{"\a\a"}, []Event{{Source: SourceBell}, {Source: SourceBell}}},
		{"bell inside window title is not a bell", []string{"\x1b]0;my title\a"}, []Event{{Source: SourceTitle, Message: "my title"}}},
		{"title with ST then bell", []string{"\x1b]2;title\x1b\\\a"}, []Event{{Source: SourceTitle, Message: "title"}, {Source: SourceBell}}},
		{"osc 9", []string{"\x1b]9;Claude needs your input\a"}, []Event{{Source: SourceOSC, Message: "Claude needs your input"}}},
		{"osc 9 split across chunks", []string{"\x1b]9;wait", "ing for approval\x1b\\"}, []Event{{Source: SourceOSC, Message: "waiting for approval"}}},
		{"osc 777 notify", []string{"\x1b]777;notify;Codex;turn complete\a"}, []Event{{Source: SourceOSC, Message: "Codex: turn complete"}}},
		{"osc 777 other", []string{"\x1b]777;other;x\a"}, nil},
		{"csi bell after sgr", []string{"\x1b[31m\a\x1b[0m"}, []Event{{Source: SourceBell}}},
		{"esc split from bracket", []string{"\x1b", "]9;split esc\a"}, []Event{{Source: SourceOSC, Message: "split esc"}}},
		{"hyperlink osc ignored", []string{"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\"}, nil},
		{"truncated message", []string{"\x1b]9;" + strings.Repeat("m", 400) + "\a"}, []Event{{Source: SourceOSC, Message: strings.Repeat("m", 200)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s Scanner
			var got []Event
			for _, ch := range c.chunks {
				got = append(got, s.Scan([]byte(ch))...)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("event %d: got %+v want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// The scanner tells a notification that ends a sequence begun in an earlier
// chunk (Carried), and a chunk that ends inside one begun before it
// (Through), so that the session can give the notification the turn its
// sequence began in.
func TestScannerTellsSequencesBegunBefore(t *testing.T) {
	var s Scanner
	for i, c := range []struct {
		chunk            string
		events           int
		carried, through bool
	}{
		{"x\x1b]9;one", 0, false, false},         // begun here
		{" two", 0, false, true},                 // through
		{" three\a\a\x1b]9;new", 2, true, false}, // ends it, a bell, begins another
		{"er\a", 1, true, false},                 // ends the one begun before
		{"\x1b]9;whole\a", 1, false, false},      // begun and ended here
		{"\x1b]9;a\x1bX", 0, false, false},       // abandoned for another sequence, begun here
		{"\x1b]9;b\a", 1, false, false},          // a new one, begun here
	} {
		got := s.Scan([]byte(c.chunk))
		if len(got) != c.events || s.Carried() != c.carried || s.Through() != c.through {
			t.Fatalf("chunk %d %q: %d events, carried %v, through %v", i, c.chunk, len(got), s.Carried(), s.Through())
		}
	}
}

func TestCleanMessage(t *testing.T) {
	if got := CleanMessage("  hi\x00there\x1b[0m  "); got != "hithere[0m" {
		t.Fatalf("got %q", got)
	}
	if got := CleanMessage(strings.Repeat("x", 600)); len(got) != MaxAttentionMessage {
		t.Fatalf("len %d", len(got))
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"  Priya Shah ":          "Priya Shah",
		"":                       "guest",
		"   ":                    "guest",
		"a\x00b\x1bc":            "abc",
		strings.Repeat("x", 100): strings.Repeat("x", 40),
		"é🙂":                     "é🙂",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanNameIsOneLineWithAGuestDefault(t *testing.T) {
	if proto.MaxNameLen != 40 {
		t.Fatalf("MaxNameLen is %d; the cases below are written for 40", proto.MaxNameLen)
	}
	for _, tc := range []struct{ in, want string }{
		{"", "guest"},
		{"  ", "guest"},
		{"\x07\x1b", "guest"},
		{"Priya", "Priya"},
		{" a\tb\nc ", "abc"},
		{"x\x7fy", "xy"},
		{strings.Repeat("é", 45), strings.Repeat("é", 40)},         // 40 runes, not 40 bytes
		{" " + strings.Repeat("é", 45), strings.Repeat("é", 40)},   // trimmed before the cut
		{strings.Repeat("a", 39) + "  b", strings.Repeat("a", 39)}, // the cut ends in a space, trimmed after
	} {
		if got := CleanName(tc.in); got != tc.want {
			t.Errorf("CleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The scanner reports a program status, OSC 7501, ended by BEL or ST, as a
// status event with its state, kind and app: Codex 0.163's kindless shape
// and 0.164's with a kind, the fields in any order, an unknown field
// ignored, a kind kept only with blocked; one split between two reads once
// whole; and one it does not understand (a state or a kind it does not
// know, no state, one cut at the capture) as the state unknown, a state
// declared, not nothing. The BEL that ends it is no bell.
func TestTheScannerReportsProgramStatuses(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   []Event
	}{
		{"0.163 blocked, no kind", []string{"\x1b]7501;state=blocked:app=codex\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusBlocked, App: "codex"}}},
		{"0.164 blocked with a kind, BEL", []string{"\x1b]7501;state=blocked:app=codex:kind=question\a"}, []Event{{Source: SourceStatus, Message: StatusBlocked, Kind: "question", App: "codex"}}},
		{"working, fields in another order, one unknown", []string{"\x1b]7501;app=codex:extra=1:state=working\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusWorking, App: "codex"}}},
		{"a kind with working is dropped", []string{"\x1b]7501;state=working:kind=permission\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusWorking}}},
		{"done, error, idle, clear", []string{"\x1b]7501;state=done:app=codex\x1b\\\x1b]7501;state=error\x1b\\\x1b]7501;state=idle\x1b\\\x1b]7501;state=clear\x1b\\"},
			[]Event{{Source: SourceStatus, Message: StatusDone, App: "codex"}, {Source: SourceStatus, Message: StatusError}, {Source: SourceStatus, Message: StatusIdle}, {Source: SourceStatus, Message: StatusClear}}},
		{"split between reads", []string{"\x1b]7501;state=bloc", "ked:app=codex:kind=auth\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusBlocked, Kind: "auth", App: "codex"}}},
		{"an unknown state", []string{"\x1b]7501;state=paused:app=codex\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusUnknown, App: "codex"}}},
		{"an unknown kind", []string{"\x1b]7501;state=blocked:app=codex:kind=review\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusUnknown, App: "codex"}}},
		{"no state", []string{"\x1b]7501;app=codex\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusUnknown, App: "codex"}}},
		{"cut at the capture", []string{"\x1b]7501;state=working:app=" + strings.Repeat("c", 400) + "\x1b\\"}, []Event{{Source: SourceStatus, Message: StatusUnknown}}},
		{"then a bell", []string{"\x1b]7501;state=working\a\a"}, []Event{{Source: SourceStatus, Message: StatusWorking}, {Source: SourceBell}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s Scanner
			var got []Event
			for _, ch := range c.chunks {
				got = append(got, s.Scan([]byte(ch))...)
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("events %+v, want %+v", got, c.want)
			}
		})
	}
}
