package session

import (
	"regexp"
	"testing"
)

// codexUpdateScreen is Codex 0.161.0's update question as it drew it in a
// PTY on 2026-10-11 (an isolated CODEX_HOME whose version.json named
// 0.162.1): the heading, the highlighted Update now, and Skip until next
// version drawn word by word with cursor moves between.
const codexUpdateScreen = "\x1b[?2026h\x1b[?25l\x1b[2;3H\x1b[1mUpdate available\x1b[22m\x1b[2m\x1b[2m \xc2\xb7 0.161.0 \xe2\x86\x92 0.162.1\x1b[3;3HRelease notes: \x1b[4m\x1b]8;;https://github.com/openai/codex/releases/latest\ahttps://github.com/openai/codex/releases/latest\x1b]8;;\a\x1b[5;1H\x1b[24m\x1b[22m\x1b[7m\x1b[1m\xe2\x80\xba 1. Update now (runs `npm install -g @openai/codex`)                                               \x1b[6;3H\x1b[27m\x1b[22m2.\x1b[6;6HSkip\x1b[7;3H3.\x1b[7;6HSkip\x1b[7;11Huntil\x1b[7;17Hnext\x1b[7;22Hversion\x1b[9;3H\x1b[1menter\x1b[22m\x1b[2m\x1b[2m continue \xc2\xb7 \x1b[22m\x1b[1mesc\x1b[22m\x1b[2m\x1b[2m skip\x1b[39m\x1b[49m\x1b[0m\x1b[?2026l"

// A question's words can be other words of its screen than the ones that
// find it (the catalog's words): Codex's update question, found by its
// highlighted "Update now (runs", shows its heading, so that "answered:"
// never reads as if Update had been picked; its "Skip until next version"
// is the digit 3 alone, which answers it without an Enter.
func TestAQuestionsWordsAreItsHeading(t *testing.T) {
	upd := []Option{{Label: "Skip this update", Input: "\x1b"}, {Label: "Skip until next version", Input: "3"}}
	s, p := newLocalWith(t, quiet(Options{Questions: []StartupQuestion{{
		Pattern: regexp.MustCompile(`(?i)update\s*now\s*\(\s*runs`),
		Words:   regexp.MustCompile(`(?i)(update\s*available)\W*?(\d+(?:\.\d+)+\s*(?:→|->)\s*\d+(?:\.\d+)+)`),
		Answers: upd,
	}}}))
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(codexUpdateScreen))
	att := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if att.State != AttentionNeedsInput || att.Message != "Update available 0.161.0 → 0.162.1" || len(att.Options) != 2 || att.Options[1] != upd[1] {
		t.Fatalf("attention %+v", att)
	}
	if err := s.Input(sub, []byte("3")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
	info := s.Info()
	if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.Message != "Update available 0.161.0 → 0.162.1" {
		t.Fatalf("the choice did not answer the question by its words: %+v %+v", info.Attention, info.LastAnswer)
	}
	// Without its words a question is told by what found it; a words pattern that matches nothing changes nothing.
	q := StartupQuestion{Pattern: regexp.MustCompile(`(?i)update\s*now\s*\(\s*runs`), Words: regexp.MustCompile(`never here`)}
	if got := q.words("› 1. Update now (runs `npm`"); got != "Update now (runs" {
		t.Fatalf("words %q", got)
	}
	q.Words = regexp.MustCompile(`(?i)release\s*notes`)
	if got := q.words("Release notes: x › 1. Update now (runs `npm`"); got != "Release notes" {
		t.Fatalf("words without a group %q", got)
	}
	if got := q.words("nothing to see"); got != "" {
		t.Fatalf("words of a screen without the question %q", got)
	}
}
