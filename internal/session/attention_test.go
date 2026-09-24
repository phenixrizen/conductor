package session

import (
	"strings"
	"testing"
)

func TestScannerEvents(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   []Event
	}{
		{"bare bell", []string{"hello\a world"}, []Event{{Source: SourceBell}}},
		{"two bells", []string{"\a\a"}, []Event{{Source: SourceBell}, {Source: SourceBell}}},
		{"bell inside window title is not a bell", []string{"\x1b]0;my title\a"}, nil},
		{"title with ST then bell", []string{"\x1b]2;title\x1b\\\a"}, []Event{{Source: SourceBell}}},
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

func TestCleanMessage(t *testing.T) {
	if got := CleanMessage("  hi\x00there\x1b[0m  "); got != "hithere[0m" {
		t.Fatalf("got %q", got)
	}
	if got := CleanMessage(strings.Repeat("x", 600)); len(got) != MaxAttentionMessage {
		t.Fatalf("len %d", len(got))
	}
}
