package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/proto"
)

func TestActivityRingKeepsNewestOldestFirst(t *testing.T) {
	var r activityRing
	for i := 0; i < MaxActivity+25; i++ {
		r.Add(ActivityEntry{Type: "link", Message: fmt.Sprintf("m%d", i)})
	}
	snap := r.Snapshot()
	if len(snap) != MaxActivity {
		t.Fatalf("len %d, want %d", len(snap), MaxActivity)
	}
	if snap[0].Message != "m25" || snap[len(snap)-1].Message != fmt.Sprintf("m%d", MaxActivity+24) {
		t.Fatalf("order: first %q last %q", snap[0].Message, snap[len(snap)-1].Message)
	}
	if r.Add(ActivityEntry{Type: "link"}); r.Snapshot()[0].Message != "m26" {
		t.Fatal("ring did not drop the oldest entry")
	}
}

func TestActivityRingStampsTime(t *testing.T) {
	var r activityRing
	r.Add(ActivityEntry{Type: "join", ByName: "Priya"})
	if r.Snapshot()[0].At.IsZero() {
		t.Fatal("At must be stamped when missing")
	}
}

func TestCleanEntryCapsFields(t *testing.T) {
	e := CleanEntry(ActivityEntry{Type: "artifact", URL: strings.Repeat("u", 3000), To: strings.Repeat("t", 100), Tool: strings.Repeat("x", 200), Message: "a\x1bb"})
	if len(e.URL) != MaxEventURL || len([]rune(e.To)) != MaxEventTo || len(e.Tool) != MaxEventTool || e.Message != "ab" {
		t.Fatalf("%+v", e)
	}
	if ValidEventType("bogus") || !ValidEventType("handoff") || !ValidEventType("join") {
		t.Fatal("ValidEventType")
	}
}

func TestValidEventType(t *testing.T) {
	for _, typ := range []string{
		ActivityAttention, ActivityInput, ActivityJoin, ActivityLeave, ActivityLink, ActivityStatus,
		ActivityProgress, ActivityArtifact, ActivityHandoff, ActivityToolUse, ActivityToolDenied, ActivityError,
	} {
		if !ValidEventType(typ) {
			t.Errorf("%q must be a valid type", typ)
		}
	}
	// Attention states are not entry types, and matching is exact.
	for _, typ := range []string{"", "bogus", "Progress", " progress", "tool-use", "needs_input", "working", "done", "clear"} {
		if ValidEventType(typ) {
			t.Errorf("%q must not be a valid type", typ)
		}
	}
}

func TestCleanEntryStripsAndTrims(t *testing.T) {
	got := CleanEntry(ActivityEntry{
		Type:    ActivityArtifact,
		ByName:  " agent\x00 ",
		Message: "  line one\nline two\x07 ",
		URL:     " https://example.com/a\r\nb\x00 ",
		To:      "\treviewer\n",
		Tool:    " Bash\x1b[31m ",
	})
	// Message and Tool follow CleanMessage, which keeps line breaks; URL, To and
	// ByName are single-line.
	want := ActivityEntry{Type: ActivityArtifact, ByName: "agent", Message: "line one\nline two", URL: "https://example.com/ab", To: "reviewer", Tool: "Bash[31m"}
	if got != want {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestCleanEntryLeavesEmptyFieldsEmpty(t *testing.T) {
	want := ActivityEntry{Type: ActivityProgress}
	if got := CleanEntry(want); got != want {
		t.Fatalf("got %+v, want the entry unchanged (no default name, nothing invented)", got)
	}
}

func TestCleanEntryCutsAtCharacterBoundaries(t *testing.T) {
	// "€" is three bytes: a byte limit that is not a multiple of three lands
	// inside a character, and the cut must back up to the one before it.
	e := CleanEntry(ActivityEntry{
		To:   strings.Repeat("é", 100), // the limit counts runes
		Tool: strings.Repeat("€", 100), // the limit counts bytes: 33 whole characters fit in 100
		URL:  strings.Repeat("€", 1000),
	})
	if got := len([]rune(e.To)); got != MaxEventTo || !utf8.ValidString(e.To) {
		t.Errorf("To: %d runes, valid %v", got, utf8.ValidString(e.To))
	}
	if len(e.Tool) != 99 || !utf8.ValidString(e.Tool) {
		t.Errorf("Tool: %d bytes, valid %v; want 99 bytes", len(e.Tool), utf8.ValidString(e.Tool))
	}
	if len(e.URL) != 2046 || !utf8.ValidString(e.URL) {
		t.Errorf("URL: %d bytes, valid %v; want 2046 bytes", len(e.URL), utf8.ValidString(e.URL))
	}
}

// JSON escapes & < > as six bytes each, so text near its limits can outgrow
// one CONTROL frame. The relay rejects such a frame and closes the host's
// connection, so a cleaned entry must always fit.
func TestCleanEntryFitsOneControlFrame(t *testing.T) {
	worst := ActivityEntry{
		Type:    ActivityArtifact,
		By:      strings.Repeat("b", IDLen),
		ByName:  strings.Repeat("&", 200),
		Message: strings.Repeat("<", 1000),
		URL:     strings.Repeat("&", 3000),
		To:      strings.Repeat(">", 100),
		Tool:    strings.Repeat("&", 300),
	}
	e := CleanEntry(worst)
	frame := proto.MustControl(activityMessage(e))
	if _, err := proto.Decode(frame); err != nil {
		t.Fatalf("cleaned entry does not fit a control frame: %v (%d bytes)", err, len(frame))
	}
	if e.URL != "" {
		t.Errorf("a URL of %d escaped bytes must be dropped, got %d bytes", 6*MaxEventURL, len(e.URL))
	}
	if e.Message == "" || e.To == "" || e.Tool == "" || e.ByName == "" {
		t.Errorf("only the URL may be dropped: %+v", e)
	}

	// A busy query string that still fits is kept whole.
	url := "https://example.com/?" + strings.Repeat("a=b&", 500)
	if got := CleanEntry(ActivityEntry{Type: ActivityArtifact, URL: url}); got.URL != url {
		t.Errorf("a URL that fits was changed: %d bytes -> %d", len(url), len(got.URL))
	}
}

// An entry is cleaned before the ring stamps it, and a stamp adds up to ten
// bytes to the message: a URL that fits only without one must not survive.
func TestCleanEntryLeavesRoomForTheTimestamp(t *testing.T) {
	unstamped, err := json.Marshal(activityMessage(ActivityEntry{Type: ActivityArtifact}))
	if err != nil {
		t.Fatal(err)
	}
	room := proto.MaxControl - len(unstamped) - len(`,"url":""`)
	e := CleanEntry(ActivityEntry{Type: ActivityArtifact, URL: strings.Repeat("&", room/6)}) // 6 bytes each once escaped
	e.At = time.Date(2026, 9, 29, 17, 14, 20, 999999999, time.UTC)                           // the longest RFC 3339 stamp
	b, err := json.Marshal(activityMessage(e))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > proto.MaxControl {
		t.Fatalf("cleaned, then stamped: %d bytes, limit %d", len(b), proto.MaxControl)
	}
}

func TestActivityRingCleansEntries(t *testing.T) {
	var r activityRing
	e := r.Add(ActivityEntry{Type: ActivityArtifact, URL: strings.Repeat("u", 3000), To: strings.Repeat("t", 100), Tool: strings.Repeat("x", 200)})
	if len(e.URL) != MaxEventURL || len([]rune(e.To)) != MaxEventTo || len(e.Tool) != MaxEventTool {
		t.Fatalf("Add returned an unbounded entry: %d %d %d", len(e.URL), len(e.To), len(e.Tool))
	}
	if s := r.Snapshot()[0]; s != e {
		t.Fatalf("stored %+v, returned %+v", s, e)
	}
}

func TestEventBucketRefillsAtTheConfiguredRate(t *testing.T) {
	var b eventBucket
	start := time.Now()
	took := func(at time.Time, attempts int) (n int) {
		for range attempts {
			if b.take(at) {
				n++
			}
		}
		return n
	}
	if got := took(start, EventBurst+10); got != EventBurst {
		t.Fatalf("a fresh bucket allows a burst of %d, took %d", EventBurst, got)
	}
	if got := took(start, 10); got != 0 {
		t.Fatalf("an empty bucket must not refill without time passing, took %d", got)
	}
	if got := took(start.Add(500*time.Millisecond), 100); got != EventRatePerSecond/2 {
		t.Fatalf("half a second refills %d, took %d", EventRatePerSecond/2, got)
	}
	// A long pause banks one burst at most.
	if got := took(start.Add(time.Hour), 3*EventBurst); got != EventBurst {
		t.Fatalf("after a long pause a burst is %d, took %d", EventBurst, got)
	}
	// A clock that steps back neither refills nor breaks the bucket.
	if got := took(start, 5); got != 0 {
		t.Fatalf("time going backwards refilled %d", got)
	}
	if got := took(start.Add(time.Hour+time.Second), 100); got != EventRatePerSecond {
		t.Fatalf("one second after the last call refills %d, took %d", EventRatePerSecond, got)
	}
}
