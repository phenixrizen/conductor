// Package operational exposes bounded operator diagnostics separately from the
// authenticated API. Metrics never label source, principals, repository IDs or URLs.
package operational

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

type Queue struct {
	Kind                string
	Pending, Unresolved int64
	OldestSeconds       float64
}
type Snapshot struct {
	Ready  bool
	Queues []Queue
}
type Source interface {
	OperationalSnapshot(context.Context) (Snapshot, error)
}
type Monitor struct {
	source   Source
	started  time.Time
	inFlight atomic.Int64
	counts   [6]atomic.Uint64
	nanos    atomic.Uint64
	samples  atomic.Uint64
	capacity chan struct{}
}

func New(source Source) *Monitor {
	return &Monitor{source: source, started: time.Now(), capacity: make(chan struct{}, 2)}
}
func ValidAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	number, parseErr := strconv.Atoi(port)
	return err == nil && parseErr == nil && number > 0 && number <= 65535 && ip != nil && ip.IsLoopback()
}
func (m *Monitor) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		m.inFlight.Add(1)
		defer m.inFlight.Add(-1)
		rec := &response{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		class := rec.status / 100
		if class < 1 || class > 5 {
			class = 5
		}
		m.counts[class].Add(1)
		m.nanos.Add(uint64(time.Since(start)))
		m.samples.Add(1)
	})
}

type response struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *response) WriteHeader(status int) {
	if !w.wrote {
		w.status = status
		w.wrote = true
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *response) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *response) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (m *Monitor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"alive"}`)
	})
	read := func(r *http.Request) (Snapshot, error) {
		select {
		case m.capacity <- struct{}{}:
			defer func() { <-m.capacity }()
		default:
			return Snapshot{}, fmt.Errorf("diagnostics busy")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		return m.source.OperationalSnapshot(ctx)
	}
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := read(r)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if err != nil || !snapshot.Ready {
			w.WriteHeader(503)
			fmt.Fprintln(w, `{"status":"unavailable"}`)
			return
		}
		fmt.Fprintln(w, `{"status":"ready"}`)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := read(r)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err != nil {
			w.WriteHeader(503)
			fmt.Fprintln(w, "# Database diagnostics unavailable")
			return
		}
		ready := 0
		if snapshot.Ready {
			ready = 1
		}
		fmt.Fprintf(w, "# TYPE conductor_database_ready gauge\nconductor_database_ready %d\n# TYPE conductor_process_uptime_seconds gauge\nconductor_process_uptime_seconds %.3f\n# TYPE conductor_http_in_flight gauge\nconductor_http_in_flight %d\n", ready, time.Since(m.started).Seconds(), m.inFlight.Load())
		fmt.Fprintln(w, "# TYPE conductor_http_requests_total counter")
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(w, "conductor_http_requests_total{status_class=\"%dxx\"} %d\n", i, m.counts[i].Load())
		}
		fmt.Fprintf(w, "# TYPE conductor_http_duration_seconds summary\nconductor_http_duration_seconds_sum %.9f\nconductor_http_duration_seconds_count %d\n", float64(m.nanos.Load())/1e9, m.samples.Load())
		fmt.Fprintln(w, "# TYPE conductor_outbox_pending gauge\n# TYPE conductor_outbox_unresolved gauge\n# TYPE conductor_outbox_oldest_pending_seconds gauge")
		for _, q := range snapshot.Queues {
			switch q.Kind {
			case "context", "coordination", "publication", "tracker", "runtime":
			default:
				continue
			}
			fmt.Fprintf(w, "conductor_outbox_pending{kind=\"%s\"} %d\nconductor_outbox_unresolved{kind=\"%s\"} %d\nconductor_outbox_oldest_pending_seconds{kind=\"%s\"} %.3f\n", q.Kind, q.Pending, q.Kind, q.Unresolved, q.Kind, q.OldestSeconds)
		}
	})
	return mux
}
