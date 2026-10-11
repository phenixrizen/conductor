package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
)

// requestLines waits until the log holds n request lines and returns them:
// the middleware writes one after the response, so a client can read the
// response before its line is there.
func requestLines(t *testing.T, logs *logBuffer, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lines []string
		for _, line := range strings.Split(logs.String(), "\n") {
			if strings.Contains(line, "msg=request ") {
				lines = append(lines, line)
			}
		}
		if len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d request lines, want %d:\n%s", len(lines), n, logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The request and panic log lines name a request by the route the mux
// matched; on a catch-all route (the join page) or none, by its path with a
// share link's token replaced. Query strings are never logged.
func TestRequestLogNamesTheRouteNotTheLinkToken(t *testing.T) {
	for _, tc := range []struct {
		name       string
		level      slog.Level
		switchyard bool
	}{
		{"serve-debug", slog.LevelDebug, false},
		{"serve-info", slog.LevelInfo, false},
		{"switchyard-debug", slog.LevelDebug, true},
		{"switchyard-info", slog.LevelInfo, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logBuffer{}
			log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: tc.level}))
			e := newTestEnvLogging(t, func(c *config.Config) { c.Switchyard.Enabled = tc.switchyard }, log)

			// Synthetic tokens, one for each place a token travels.
			const (
				joinPage   = "SYNTHJOINPAGE0000000000000000000"
				joinUpper  = "SYNTHJOINUPPER000000000000000000"
				joinAPI    = "SYNTHJOINAPI00000000000000000000"
				joinPost   = "SYNTHJOINPOST0000000000000000000"
				queryWS    = "SYNTHQUERYWS00000000000000000000"
				queryEvt   = "SYNTHQUERYEVENTS0000000000000000"
				queryFiles = "SYNTHQUERYFILES00000000000000000"
				panicBare  = "SYNTHPANICBARE000000000000000000"
				panicRoute = "SYNTHPANICROUTE00000000000000000"
				panicQuery = "SYNTHPANICQUERY00000000000000000"
			)
			secrets := []string{joinPage, joinUpper, joinAPI, joinPost, queryWS, queryEvt, queryFiles, panicBare, panicRoute, panicQuery}
			// A live token the server minted (a switchyard launches no
			// session, so a link on an unknown session id is enough for the
			// join route to resolve it).
			_, live, err := e.srv.links.Create("sess-for-log-test", "view", "log test", 0)
			if err != nil {
				t.Fatal(err)
			}
			secrets = append(secrets, live)

			gets := []string{
				"/join/" + joinPage,               // the join page: the app, or the switchyard's front
				"/JOIN/" + joinUpper + "/more",    // the app's router matches paths in any case
				"/api/join/" + joinAPI,            // the join route, an unknown token
				"/api/join/" + live,               // the join route, a live token
				"/ws/sessions/x?token=" + queryWS, // the viewer socket's token (not upgraded here)
				"/api/events?token=" + queryEvt,   // query tokens elsewhere
				"/api/sessions/x/files?path=a&raw=1&token=" + queryFiles,
			}
			for _, p := range gets {
				resp, err := e.client.Get(e.http.URL + p)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
			}
			// A POST on the join path is no route of its own: the /api/
			// catch-all answers it.
			resp, err := e.client.Post(e.http.URL+"/api/join/"+joinPost, "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			// Handlers that panic behind the same middleware: one with no
			// route (an empty pattern), one behind the join route's pattern.
			bare := e.srv.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
			bare.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/join/"+panicBare+"?token="+panicQuery, nil))
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/join/{token}", func(http.ResponseWriter, *http.Request) { panic("boom") })
			routed := e.srv.middleware(mux)
			routed.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/join/"+panicRoute+"?token="+panicQuery, nil))

			if tc.level == slog.LevelDebug {
				lines := requestLines(t, logs, len(gets)+3)
				for _, want := range []string{"path=/join/{token}", "path=/JOIN/{token}", "path=/api/join/{token}", "path=/ws/sessions/{id}", "path=/api/events", "path=/api/sessions/{id}/files"} {
					found := false
					for _, line := range lines {
						found = found || strings.Contains(line, want+" ")
					}
					if !found {
						t.Errorf("no request line with %s:\n%s", want, strings.Join(lines, "\n"))
					}
				}
			}
			out := logs.String()
			if got := strings.Count(out, `msg="panic in handler" path=/api/join/{token} `); got != 2 {
				t.Errorf("%d panic lines naming /api/join/{token}, want 2:\n%s", got, out)
			}
			for _, s := range secrets {
				if strings.Contains(out, s) {
					t.Errorf("token %q in the log at %s:\n%s", s, tc.level, out)
				}
			}
		})
	}
}

// The request line after a panic records the 500 the client got.
func TestRequestLogRecordsTheStatusAfterAPanic(t *testing.T) {
	logs := &logBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e := newTestEnvLogging(t, nil, log)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec := httptest.NewRecorder()
	e.srv.middleware(mux).ServeHTTP(rec, httptest.NewRequest("GET", "/api/boom", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"internal"`) {
		t.Fatalf("reply %d %s", rec.Code, rec.Body.String())
	}
	lines := requestLines(t, logs, 1)
	if len(lines) != 1 || !strings.Contains(lines[0], "path=/api/boom ") || !strings.Contains(lines[0], "status=500 ") {
		t.Fatalf("request line after a panic:\n%s", logs.String())
	}
}

func TestLogPath(t *testing.T) {
	for _, tc := range []struct{ pattern, path, want string }{
		{"GET /api/join/{token}", "/api/join/abc", "/api/join/{token}"},
		{"GET /api/sessions/{id}/files", "/api/sessions/s1/files", "/api/sessions/{id}/files"},
		{"GET /api/health", "/api/health", "/api/health"},
		{"POST /api/runs/{run}/members/{name}/start", "/api/runs/r1/members/a/start", "/api/runs/{run}/members/{name}/start"},
		// The catch-alls and no route: the path, a link's token replaced.
		{"/", "/join/abc", "/join/{token}"},
		{"/", "/join/abc/", "/join/{token}"},
		{"/", "/Join/abc/more", "/Join/{token}"},
		{"/", "/x/../JOIN/abc", "/x/../JOIN/{token}"},
		{"/api/", "/api/join/abc", "/api/join/{token}"},
		{"", "/api/join/abc", "/api/join/{token}"},
		{"", "/join/abc", "/join/{token}"},
		{"/", "/join", "/join"},
		{"/", "/_nuxt/entry.js", "/_nuxt/entry.js"},
		{"/", "/sessions/s1", "/sessions/s1"},
		{"/ws/", "/ws/other", "/ws/other"},
		{"", "/nowhere", "/nowhere"},
	} {
		r := httptest.NewRequest("GET", "http://example.test"+tc.path, nil)
		r.Pattern = tc.pattern
		if got := logPath(r); got != tc.want {
			t.Errorf("logPath(%q, %q) = %q, want %q", tc.pattern, tc.path, got, tc.want)
		}
	}
}
