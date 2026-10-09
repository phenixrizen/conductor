package proto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(buf.Bytes())
}

// The messages have the documented shapes; a lines event's fields stay out
// when unset so the envelope is small.
func TestNvimMessageShapes(t *testing.T) {
	open := mustJSON(t, NvimOpen{T: CtlNvimOpen, ReqID: "r1", Path: "internal/api/users.go"})
	if string(open) != `{"t":"nvim_open","reqId":"r1","path":"internal/api/users.go"}` {
		t.Fatalf("open: %s", open)
	}
	in := mustJSON(t, NvimInput{T: CtlNvimInput, ID: "e1", Keys: "dd<Esc>"})
	if string(in) != `{"t":"nvim_input","id":"e1","keys":"dd<Esc>"}` {
		t.Fatalf("input: %s", in)
	}
	ev := mustJSON(t, NvimEvent{T: CtlNvimEvent, ID: "e1", Kind: NvimCursor, Line: 3, Col: 1, Mode: "n"})
	if string(ev) != `{"t":"nvim_event","id":"e1","kind":"cursor","line":3,"col":1,"mode":"n"}` {
		t.Fatalf("cursor: %s", ev)
	}
	var back NvimEvent
	if err := json.Unmarshal(ev, &back); err != nil || back.Kind != NvimCursor || back.Line != 3 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	// The buffer's changes not written: the field only while there are some.
	if m := mustJSON(t, NvimEvent{T: CtlNvimEvent, ID: "e1", Kind: NvimModified, Modified: true}); string(m) != `{"t":"nvim_event","id":"e1","kind":"modified","modified":true}` {
		t.Fatalf("modified: %s", m)
	}
	if m := mustJSON(t, NvimEvent{T: CtlNvimEvent, ID: "e1", Kind: NvimModified}); string(m) != `{"t":"nvim_event","id":"e1","kind":"modified"}` {
		t.Fatalf("not modified: %s", m)
	}
	// A close keeps the swap file unless it discards; an older owner reads
	// the discard as a plain close.
	if c := mustJSON(t, NvimClose{T: CtlNvimClose, ID: "e1"}); string(c) != `{"t":"nvim_close","id":"e1"}` {
		t.Fatalf("close: %s", c)
	}
	c := mustJSON(t, NvimClose{T: CtlNvimClose, ID: "e1", Discard: true})
	if string(c) != `{"t":"nvim_close","id":"e1","discard":true}` {
		t.Fatalf("close discarding: %s", c)
	}
	var older struct {
		T  string `json:"t"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(c, &older); err != nil || older.ID != "e1" {
		t.Fatalf("an older owner's read: %+v %v", older, err)
	}
}

// A big change is cut into events that each fit MaxControl, the first with
// the change's range and the rest inserting after it, in order; a line past
// MaxNvimLine is cut and marked; a deletion is one event with no lines.
func TestNvimLineEventsFitTheBound(t *testing.T) {
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, strings.Repeat("x", 60)+` "quoted" \ `)
	}
	evs := NvimLineEvents("e1", 10, 12, lines)
	if len(evs) < 4 {
		t.Fatalf("%d events for %d lines", len(evs), len(lines))
	}
	if evs[0].First != 10 || evs[0].Last != 12 {
		t.Fatalf("first event range %d %d", evs[0].First, evs[0].Last)
	}
	at, total := 10, 0
	for i, ev := range evs {
		raw := MustControl(ev)
		if len(raw) > MaxControl {
			t.Fatalf("event %d is %d bytes", i, len(raw))
		}
		if i > 0 && (ev.First != at || ev.Last != at) {
			t.Fatalf("event %d inserts at %d-%d, want %d", i, ev.First, ev.Last, at)
		}
		at += len(ev.Lines)
		total += len(ev.Lines)
	}
	if total != len(lines) {
		t.Fatalf("%d lines carried, want %d", total, len(lines))
	}
	long := NvimLineEvents("e1", 0, 1, []string{strings.Repeat("é", MaxNvimLine)})
	if len(long) != 1 || !long[0].Truncated || len(long[0].Lines[0]) > MaxNvimLine || !strings.HasSuffix(long[0].Lines[0], "é") {
		t.Fatalf("a long line: %+v", long[0].Truncated)
	}
	del := NvimLineEvents("e1", 4, 6, nil)
	if len(del) != 1 || del[0].First != 4 || del[0].Last != 6 || len(del[0].Lines) != 0 {
		t.Fatalf("a deletion: %+v", del)
	}
}
