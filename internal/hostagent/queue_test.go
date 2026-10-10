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
		if !q.add(nil, job(i)) {
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
		if !q.add(nil, func() { close(done) }) {
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
	q.add(untilGone, func() { <-release })
	q.add(untilGone, note("gone 1"))
	q.add(here, note("here 1"))
	q.add(untilGone, note("gone 2"))
	q.add(here, note("here 2"))
	gone.Store(true)
	close(release)
	q.wait()
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[0] != "here 1" || ran[1] != "here 2" {
		t.Fatalf("ran %v", ran)
	}
}

// close drops what waits and refuses what comes after it; the request
// running finishes, and then nothing of the queue runs.
func TestRequestQueueCloseDropsWhatWaits(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	finished := make(chan struct{})
	q.add(nil, func() {
		<-release
		close(finished)
	})
	for range maxQueued {
		q.add(nil, func() { t.Error("a waiting request ran after close") })
	}
	q.close()
	if q.add(nil, func() { t.Error("a request added after close ran") }) {
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
