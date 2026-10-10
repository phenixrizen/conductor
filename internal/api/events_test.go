package api

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

func TestEventHubActivityFormat(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityArtifact, ByName: "agent", Message: "PR", URL: "https://x/1"}, "")
	want := "event: activity\n" +
		`data: {"sessionId":"s1","at":"2026-09-29T12:00:00Z","type":"artifact","byName":"agent","message":"PR","url":"https://x/1"}` + "\n\n"
	select {
	case got := <-ch:
		if string(got) != want {
			t.Fatalf("got\n%s\nwant\n%s", got, want)
		}
	default:
		t.Fatal("nothing queued")
	}
}

// An entry may hold line breaks; the SSE message must stay one event of one
// data line, whatever the text says.
func TestEventHubActivityStaysOneMessage(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	h.activity("s1", session.ActivityEntry{Type: session.ActivityError, Message: "a\nb\r\nc d\n\ndata: forged\nevent: session"}, "")
	msg := string(<-ch)
	if strings.Count(msg, "\n") != 3 || !strings.HasSuffix(msg, "\n\n") || !strings.HasPrefix(msg, "event: activity\ndata: {") {
		t.Fatalf("message is not one event: %q", msg)
	}
}

// A browser tab that stopped reading must not hold up the recording
// goroutine (OnActivity runs on it), must not lose its stream to a burst of
// activity, and must keep room for the session changes it cannot do without.
func TestEventHubActivityNeverBlocksOrEvictsAClientThatStoppedReading(t *testing.T) {
	h := newEventHub()
	stalled := h.subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20*eventQueue; i++ {
			h.activity("s", session.ActivityEntry{Type: session.ActivityProgress, Message: "n"}, "")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("activity blocked on a client that is not reading")
	}
	if n := len(stalled); n != activityQueueLimit {
		t.Fatalf("queue holds %d entries, want it held at %d", n, activityQueueLimit)
	}
	h.publish(session.Info{ID: "s", Name: "n"})
	h.removed("s")
	if n := len(stalled); n != activityQueueLimit+2 {
		t.Fatalf("session changes were not queued behind the entries: %d", n)
	}
	for i := 0; i < activityQueueLimit+2; i++ {
		select {
		case msg, ok := <-stalled:
			if !ok {
				t.Fatalf("the client was evicted after %d messages", i)
			}
			want := "event: activity"
			switch i {
			case activityQueueLimit:
				want = "event: session"
			case activityQueueLimit + 1:
				want = "event: removed"
			}
			if !strings.HasPrefix(string(msg), want) {
				t.Fatalf("message %d is %.30q, want %s", i, msg, want)
			}
		default:
			t.Fatalf("queue ran dry after %d messages", i)
		}
	}
}

func TestEventHubActivityResumesOnceTheClientCatchesUp(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	for i := 0; i < 2*eventQueue; i++ {
		h.activity("s", session.ActivityEntry{Type: session.ActivityProgress}, "")
	}
	for len(ch) > 0 {
		<-ch
	}
	h.activity("s", session.ActivityEntry{Type: session.ActivityError, Message: "after"}, "")
	select {
	case msg := <-ch:
		if !strings.Contains(string(msg), `"message":"after"`) {
			t.Fatalf("got %s", msg)
		}
	default:
		t.Fatal("a client that caught up got nothing")
	}
}

// One client that does not read costs the others nothing.
func TestEventHubActivityReachesEveryClientThatReads(t *testing.T) {
	h := newEventHub()
	stalled := h.subscribe()
	fast := h.subscribe()
	var got atomic.Int64
	go func() {
		for range fast {
			got.Add(1)
		}
	}()
	const batches, perBatch = 10, 100
	for b := 1; b <= batches; b++ {
		for i := 0; i < perBatch; i++ {
			h.activity("s", session.ActivityEntry{Type: session.ActivityProgress}, "")
		}
		deadline := time.Now().Add(5 * time.Second)
		for got.Load() < int64(b*perBatch) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got.Load() != int64(b*perBatch) {
			t.Fatalf("the reading client has %d of %d entries", got.Load(), b*perBatch)
		}
	}
	if len(stalled) != activityQueueLimit {
		t.Fatalf("stalled client holds %d", len(stalled))
	}
}

// OnActivity runs on the goroutines that record, at the same time, and the
// hub also serves clients that come and go.
func TestEventHubIsSafeForConcurrentUse(t *testing.T) {
	h := newEventHub()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch := h.subscribe()
				for i := 0; i < 10; i++ {
					select {
					case <-ch:
					default:
					}
				}
				h.unsubscribe(ch)
			}
		}()
	}
	var producers sync.WaitGroup
	for p := 0; p < 8; p++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for i := 0; i < 300; i++ {
				h.activity("s", session.ActivityEntry{Type: session.ActivityToolUse, Tool: "Bash"}, "")
				if i%50 == 0 {
					h.publish(session.Info{ID: "s"})
					h.removed("s")
				}
			}
		}()
	}
	producers.Wait()
	close(stop)
	wg.Wait()
}

// Sinks receive every activity entry, the session it belongs to and the
// attention state it was handed with, after the clients have the entry, whether
// or not any client listens. Clients get the entry with that state on it.
func TestEventHubSinksGetEveryEntryAfterTheClients(t *testing.T) {
	h := newEventHub()
	type got struct {
		id      string
		e       session.ActivityEntry
		state   session.AttentionState
		clients int
	}
	var sunk []got
	ch := h.subscribe()
	h.addSink(func(id string, e session.ActivityEntry, state session.AttentionState) {
		sunk = append(sunk, got{id, e, state, len(ch)})
	})
	e := session.ActivityEntry{At: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), Type: session.ActivityAttention, Message: "approve?"}
	h.activity("s1", e, session.AttentionNeedsInput)
	if msg := string(<-ch); !strings.Contains(msg, `"state":"needs_input"`) || !strings.Contains(msg, `"message":"approve?"`) {
		t.Fatalf("a client was sent %q", msg)
	}
	h.unsubscribe(ch)
	h.activity("s2", e, "")
	if len(sunk) != 2 || sunk[0] != (got{"s1", e, session.AttentionNeedsInput, 1}) || sunk[1] != (got{"s2", e, "", 0}) {
		t.Fatalf("sunk %+v", sunk)
	}
}

// An attention entry's event carries the state it records, so a client types
// it without waiting for the session change; other entries carry none, and
// neither does an attention entry with a state that is not one of the three.
func TestEventHubActivityCarriesTheAttentionState(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityAttention, Message: "Allow Bash?"}, session.AttentionNeedsInput)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityProgress, Message: "1/3"}, session.AttentionNeedsInput)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityAttention, Message: "x"}, "bogus")
	for _, want := range []string{
		`{"sessionId":"s1","at":"2026-10-01T09:00:00Z","type":"attention","message":"Allow Bash?","state":"needs_input"}`,
		`{"sessionId":"s1","at":"2026-10-01T09:00:00Z","type":"progress","message":"1/3"}`,
		`{"sessionId":"s1","at":"2026-10-01T09:00:00Z","type":"attention","message":"x"}`,
	} {
		if got := string(<-ch); got != "event: activity\ndata: "+want+"\n\n" {
			t.Fatalf("got %q, want data %s", got, want)
		}
	}
}

// A run event names the run and nothing more, and is dropped, never cut,
// past its bound.
func TestEventHubRunEvent(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	h.run("api-sweep-0123abcd", false)
	h.run("api-sweep-0123abcd", true)
	h.run(strings.Repeat("x", maxRunEvent), false)
	for _, want := range []string{
		"event: run\ndata: {\"id\":\"api-sweep-0123abcd\"}\n\n",
		"event: run\ndata: {\"id\":\"api-sweep-0123abcd\",\"removed\":true}\n\n",
	} {
		if got := string(<-ch); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if len(ch) != 0 {
		t.Fatalf("an event over %d bytes was sent", maxRunEvent)
	}
}

// A chat message goes to the clients as a `chat` event naming its session or
// its run, as droppable as activity and reaching no sink.
func TestEventHubChatFormat(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	var sunk atomic.Int32
	h.addSink(func(string, session.ActivityEntry, session.AttentionState) { sunk.Add(1) })
	at := time.Date(2026, 10, 6, 8, 32, 0, 0, time.UTC)
	h.chat("s1", "", session.ChatMessage{ID: "m1", At: at, Scope: "session", Kind: "message", By: session.ChatBy{ID: "v1", Name: "Nate", Role: session.RoleControl}, Text: "hello <all>", Nonce: "n1"})
	want := "event: chat\n" +
		`data: {"sessionId":"s1","t":"chat","id":"m1","at":"2026-10-06T08:32:00Z","scope":"session","kind":"message","by":{"id":"v1","name":"Nate","role":"control"},"text":"hello \u003call\u003e","nonce":"n1"}` + "\n\n"
	select {
	case got := <-ch:
		if string(got) != want {
			t.Fatalf("got\n%s\nwant\n%s", got, want)
		}
	default:
		t.Fatal("nothing queued")
	}
	h.chat("", "r1", session.ChatMessage{ID: "m2", At: at, Scope: "run", Kind: "sent_to_agent", By: session.ChatBy{ID: "v1", Name: "Nate", Role: session.RoleControl}, Ref: "m1", To: "core"})
	if got := string(<-ch); !strings.HasPrefix(got, "event: chat\ndata: {\"runId\":\"r1\",") || !strings.Contains(got, `"to":"core"`) {
		t.Fatalf("run chat event %q", got)
	}
	if sunk.Load() != 0 {
		t.Fatal("a chat message reached a sink")
	}
}

// publishIf asks whether the session is listed under the hub's lock, which
// removed takes too: a removal that lands while a change is being published
// comes after it, and a change asked about after the removal is dropped. So
// no session event follows a session's removed event.
func TestEventHubPublishIfOrdersAgainstRemoved(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	var listed atomic.Bool
	listed.Store(true)
	entered, release, published := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(published)
		h.publishIf(session.Info{ID: "s"}, func(string) bool {
			v := listed.Load() // listed as it is asked
			close(entered)
			<-release
			return v
		})
	}()
	<-entered
	// The session leaves the registry, then its removed event goes: it
	// waits for the publication under way, and comes after it.
	listed.Store(false)
	removed := make(chan struct{})
	go func() {
		defer close(removed)
		h.removed("s")
	}()
	select {
	case <-removed:
		t.Fatal("the removed event overtook a publication under way")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-published
	<-removed
	// A change asked about after the removal is dropped.
	h.publishIf(session.Info{ID: "s"}, func(string) bool { return listed.Load() })
	var got []string
	for len(ch) > 0 {
		got = append(got, strings.SplitN(string(<-ch), "\n", 2)[0])
	}
	if strings.Join(got, ",") != "event: session,event: removed" {
		t.Fatalf("events %v, want the session's change, then its removal, then nothing", got)
	}
}
