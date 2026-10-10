package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a per-client token bucket keyed by remote IP.
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate, burst float64) *rateLimiter {
	rl := &rateLimiter{rate: rate, burst: burst, buckets: map[string]*bucket{}, now: time.Now}
	go rl.sweep()
	return rl
}

// allow consumes one token for key if available.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	}
	b.tokens = min(rl.burst, b.tokens+now.Sub(b.last).Seconds()*rl.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *rateLimiter) sweep() {
	for range time.Tick(time.Minute) {
		rl.mu.Lock()
		cutoff := rl.now().Add(-5 * time.Minute)
		for k, b := range rl.buckets {
			if b.last.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// clientKey derives the limiter key from the direct peer address. Proxy
// headers are deliberately ignored; deploy behind a proxy that limits itself.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// addrCounter counts what one address holds at a time (an open host's live
// sessions on a switchyard): acquire takes one place under max, release
// gives it back.
type addrCounter struct {
	mu   sync.Mutex
	held map[string]int
}

func (c *addrCounter) acquire(key string, max int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil {
		c.held = map[string]int{}
	}
	if c.held[key] >= max {
		return false
	}
	c.held[key]++
	return true
}

// addrBuckets shares one byte bucket among the connections of an address:
// the open hosts of one address on a switchyard relay under one bound, so
// holding openHostSessions sessions buys no more relay than one. acquire hands
// out the address's bucket, made at the rate when the address holds none;
// the viewers of hosted sessions share theirs the same way. After the last
// holder releases it the bucket is kept until it has refilled, and then
// dropped, so an address that left soon costs nothing, and one that leaves
// and comes back finds what it spent still spent rather than a fresh burst.
type addrBuckets struct {
	mu   sync.Mutex
	held map[string]*sharedBucket
}

type sharedBucket struct {
	bucket *byteBucket
	n      int
	// waiting says a check is set for when the bucket will be full (forget):
	// one at a time, however often the address comes and goes meanwhile.
	waiting bool
}

// forget drops sb, which nobody holds, once its bucket is full again: at
// once when it is, else when it will be, unless it is held again by then.
// Called with mu held.
func (c *addrBuckets) forget(key string, sb *sharedBucket) {
	if sb.waiting {
		return
	}
	wait := sb.bucket.untilFull(time.Now())
	if wait <= 0 {
		delete(c.held, key)
		return
	}
	sb.waiting = true
	time.AfterFunc(wait, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		sb.waiting = false
		if c.held[key] == sb && sb.n == 0 {
			c.forget(key, sb)
		}
	})
}

func (c *addrBuckets) acquire(key string, bytesPerSecond int) *byteBucket {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil {
		c.held = map[string]*sharedBucket{}
	}
	sb := c.held[key]
	if sb == nil {
		sb = &sharedBucket{bucket: newByteBucket(bytesPerSecond)}
		c.held[key] = sb
	}
	sb.n++
	return sb.bucket
}

func (c *addrBuckets) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sb := c.held[key]; sb != nil {
		if sb.n--; sb.n <= 0 {
			sb.n = 0
			c.forget(key, sb)
		}
	}
}

func (c *addrCounter) release(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held[key] <= 1 {
		delete(c.held, key)
		return
	}
	c.held[key]--
}
