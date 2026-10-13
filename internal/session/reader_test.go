package session

import (
	"strings"
	"testing"
)

// The key reader reads a terminal's report and a mouse report as
// typesSomething does (reportLen, one grammar): whole, one key, and none
// for a person's typing; an X10 mouse report (ESC [ M and three bytes of
// 32 or more) cut short is carried, and with a byte under 32 it is no
// mouse report, that byte a key a person typed.
func TestTheKeyReaderReadsReportsAsTypesSomethingDoes(t *testing.T) {
	for in, want := range map[string][]string{
		"\x1b[12;40R":     {"\x1b[12;40R"},
		"\x1b[<0;10;5M":   {"\x1b[<0;10;5M"},
		"\x1b[0;10;5M":    {"\x1b[0;10;5M"},
		"\x1b[M 1!":       {"\x1b[M 1!"},
		"\x1b[M\r12":      {"\x1b[M", "\r", "1", "2"},
		"y\x1b[<0;10;5My": {"y", "\x1b[<0;10;5M", "y"},
		"\x1b[?1u":        {"\x1b[?1u"},
	} {
		var r keyReader
		var got []string
		for _, k := range r.keys([]byte(in)) {
			got = append(got, string(k))
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("keys(%q) = %q, want %q", in, got, want)
		}
		typed, _ := typesSomething(nil, []byte(in))
		person := false
		for _, k := range got {
			if reportLen([]byte(k)) != len(k) {
				person = true
			}
		}
		if typed != person {
			t.Errorf("%q: typesSomething says typed %v, the key reader's keys %v", in, typed, person)
		}
	}
	var r keyReader
	if got := r.keys([]byte("\x1b[M ")); len(got) != 0 {
		t.Fatalf("an X10 report cut: %q", got)
	}
	if got := r.keys([]byte("1!")); len(got) != 1 || string(got[0]) != "\x1b[M 1!" {
		t.Fatalf("its end: %q", got)
	}
}
