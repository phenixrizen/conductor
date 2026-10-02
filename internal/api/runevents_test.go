package api

import (
	"testing"
	"time"
)

// Run changes reach the event stream as run events: the launch, and the run
// forgotten as removed.
func TestRunEventsReachTheStream(t *testing.T) {
	e := newTestEnv(t, nil)
	ch := e.srv.events.subscribe()
	defer e.srv.events.unsubscribe(ch)
	runID := e.launchCrew(t, "Evented", catMember("a", "immediately"))
	want := `event: run` + "\ndata: " + `{"id":"` + runID + `"}` + "\n\n"
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			if string(msg) == want {
				e.srv.events.run(runID, true)
				for got := range ch {
					if string(got) == `event: run`+"\ndata: "+`{"id":"`+runID+`","removed":true}`+"\n\n" {
						return
					}
				}
				t.Fatal("no removed event")
			}
		case <-deadline:
			t.Fatal("no run event")
		}
	}
}
