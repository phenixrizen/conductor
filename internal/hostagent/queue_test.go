package hostagent

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A queue runs its requests one at a time in the order added, holds at
// most maxQueued behind the one running, and refuses the rest at once
// without running them.
func TestRequestQueueRunsInOrderAndRefusesPastItsBound(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	var mu sync.Mutex
	var ran []int
	var running, most int
	job := func(i int) func() {
		return func() {
			mu.Lock()
			running++
			most = max(most, running)
			mu.Unlock()
			if i == 0 {
				<-release
			}
			mu.Lock()
			running--
			ran = append(ran, i)
			mu.Unlock()
		}
	}
	before := runtime.NumGoroutine()
	var refused []int
	for i := range maxQueued + 10 {
		if !q.add(request{run: job(i)}) {
			refused = append(refused, i)
		}
	}
	if n := runtime.NumGoroutine() - before; n > 1 {
		t.Errorf("%d goroutines for one queue", n)
	}
	if len(refused) != 9 || refused[0] != maxQueued+1 {
		t.Fatalf("refused %v", refused)
	}
	close(release)
	q.wait()
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != maxQueued+1 || most != 1 {
		t.Fatalf("ran %v, at most %d at once", ran, most)
	}
	for i, n := range ran {
		if n != i {
			t.Fatalf("ran %v", ran)
		}
	}
}

// An idle queue holds no goroutine; the next request starts one again.
func TestRequestQueueEndsItsGoroutineWhenIdle(t *testing.T) {
	var q requestQueue
	for range 3 {
		done := make(chan struct{})
		if !q.add(request{run: func() { close(done) }}) {
			t.Fatal("refused on an idle queue")
		}
		<-done
		q.wait()
		q.mu.Lock()
		running := q.running
		q.mu.Unlock()
		if running {
			t.Fatal("the queue runs with nothing to run")
		}
	}
}

// A waiting request whose viewer is gone when its turn comes is dropped;
// the others run, in order.
func TestRequestQueueDropsWhatWaitsForAViewerGone(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	var gone atomic.Bool
	untilGone := func() bool { return !gone.Load() }
	here := func() bool { return true }
	var mu sync.Mutex
	var ran []string
	note := func(s string) func() {
		return func() {
			mu.Lock()
			ran = append(ran, s)
			mu.Unlock()
		}
	}
	q.add(request{run: func() { <-release }, live: untilGone})
	q.add(request{run: note("gone 1"), live: untilGone})
	q.add(request{run: note("here 1"), live: here})
	q.add(request{run: note("gone 2"), live: untilGone})
	q.add(request{run: note("here 2"), live: here})
	gone.Store(true)
	close(release)
	q.wait()
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[0] != "here 1" || ran[1] != "here 2" {
		t.Fatalf("ran %v", ran)
	}
}

// A waiting request whose time runs out is refused then, though the one
// running holds the queue, and makes room; one whose turn came first is
// not refused by its timer.
func TestRequestQueueRefusesAWaitingRequestWhoseTimeRunsOut(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	q.add(request{run: func() { <-release }})
	expired := make(chan int, maxQueued)
	for i := range maxQueued {
		q.add(request{
			run:      func() { t.Errorf("request %d ran after its time", i) },
			deadline: time.Now().Add(50 * time.Millisecond),
			expired:  func() { expired <- i },
		})
	}
	for range maxQueued {
		select {
		case <-expired:
		case <-time.After(5 * time.Second):
			t.Fatal("a waiting request was not refused when its time ran out")
		}
	}
	ran := make(chan struct{}, maxQueued)
	for range maxQueued {
		if !q.add(request{run: func() { ran <- struct{}{} }, deadline: time.Now().Add(time.Hour), expired: func() { t.Error("refused in its time") }}) {
			t.Fatal("the room the refused requests left was not taken")
		}
	}
	if q.add(request{run: func() {}}) {
		t.Fatal("the queue took one past its bound")
	}
	close(release)
	for range maxQueued {
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatal("a waiting request did not run")
		}
	}
	q.wait()
}

// A full queue does not count the waiting requests whose viewer is gone:
// a viewer that moved to the relay and attached again is not refused for
// what its old attachment left waiting.
func TestRequestQueueMakesRoomOfWhatWaitsForAViewerGone(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	q.add(request{run: func() { <-release }})
	var gone atomic.Bool
	old := func() bool { return !gone.Load() }
	for range maxQueued {
		q.add(request{run: func() { t.Error("a request of the old attachment ran") }, live: old})
	}
	if q.add(request{run: func() {}}) {
		t.Fatal("the queue took one past its bound")
	}
	gone.Store(true)
	ran := make(chan struct{})
	if !q.add(request{run: func() { close(ran) }}) {
		t.Fatal("refused for what waits for a viewer gone")
	}
	close(release)
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the new attachment's request did not run")
	}
	q.wait()
}

// close drops what waits and refuses what comes after it; the request
// running finishes, and then nothing of the queue runs.
func TestRequestQueueCloseDropsWhatWaits(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	finished := make(chan struct{})
	q.add(request{run: func() {
		<-release
		close(finished)
	}})
	for range maxQueued {
		q.add(request{run: func() { t.Error("a waiting request ran after close") }})
	}
	q.close()
	if q.add(request{run: func() { t.Error("a request added after close ran") }}) {
		t.Fatal("a closed queue took a request")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the running request did not finish")
	}
	q.wait()
	q.mu.Lock()
	running, waiting := q.running, len(q.waiting)
	q.mu.Unlock()
	if running || waiting != 0 {
		t.Fatalf("after close and wait: running %v, %d waiting", running, waiting)
	}
}
