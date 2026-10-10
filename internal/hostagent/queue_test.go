package hostagent

import (
	"runtime"
	"sync"
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
		if !q.add(job(i)) {
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
		if !q.add(func() { close(done) }) {
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

// close drops what waits and refuses what comes after it; the request
// running finishes, and then nothing of the queue runs.
func TestRequestQueueCloseDropsWhatWaits(t *testing.T) {
	var q requestQueue
	release := make(chan struct{})
	finished := make(chan struct{})
	q.add(func() {
		<-release
		close(finished)
	})
	for range maxQueued {
		q.add(func() { t.Error("a waiting request ran after close") })
	}
	q.close()
	if q.add(func() { t.Error("a request added after close ran") }) {
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
