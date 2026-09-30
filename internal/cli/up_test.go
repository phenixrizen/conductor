package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A token no test output may contain.
const secretToken = "sekret-admin-token"

// request is what a stub server saw of one request.
type request struct {
	method, path, query, auth string
}

// stubServer answers every request with status and body, and records the
// requests it gets.
func stubServer(t *testing.T, status int, body string) (*httptest.Server, *[]request) {
	t.Helper()
	var seen []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, request{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization")})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// command is the signature of runUp and runCrews.
type command func(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error)

// runWith runs fn with args and returns what it printed.
func runWith(t *testing.T, fn command, args ...string) (code int, stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	code, err = fn(t.Context(), args, &out, &errOut)
	return code, out.String(), errOut.String(), err
}

func up(t *testing.T, args ...string) (int, string, string, error) {
	t.Helper()
	return runWith(t, runUp, args...)
}

func crews(t *testing.T, args ...string) (int, string, string, error) {
	t.Helper()
	return runWith(t, runCrews, args...)
}

// noSecret fails the test when the token is in anything a command printed.
func noSecret(t *testing.T, parts ...any) {
	t.Helper()
	for _, p := range parts {
		s := ""
		switch v := p.(type) {
		case string:
			s = v
		case error:
			if v != nil {
				s = v.Error()
			}
		}
		if strings.Contains(s, secretToken) {
			t.Fatalf("the token is in %q", s)
		}
	}
}

const launchReply = `{"run":{"id":"r-1","crewId":"crew-1","name":"Ship it","members":[{"name":"lead","sessionId":"s-1"}],"extra":{"unknown":true}}}`

// conductor up posts to the crew's launch route with the token as a bearer
// and prints the run and its URL, the server as given without a trailing slash.
func TestUpLaunchesAndPrintsRunURL(t *testing.T) {
	clearConductorEnv(t)
	srv, seen := stubServer(t, http.StatusCreated, launchReply)
	code, stdout, stderr, err := up(t, "crew-1", "--server", srv.URL+"/", "--token", secretToken)
	if code != 0 || err != nil {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	want := "run r-1\n" + srv.URL + "/runs/r-1\n"
	if stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", stdout, want)
	}
	if len(*seen) != 1 {
		t.Fatalf("requests: %+v", *seen)
	}
	got := (*seen)[0]
	if got.method != http.MethodPost || got.path != "/api/crews/crew-1/launch" || got.auth != "Bearer "+secretToken || got.query != "" {
		t.Fatalf("request: %+v", got)
	}
	noSecret(t, stdout, stderr)
}

// The server and the token come from the environment when no flag gives them,
// and a flag after the crew reads as well as one before it.
func TestUpEnvironmentAndFlagOrder(t *testing.T) {
	clearConductorEnv(t)
	srv, seen := stubServer(t, http.StatusCreated, launchReply)
	t.Setenv("CONDUCTOR_SERVER", srv.URL)
	t.Setenv("CONDUCTOR_ADMIN_TOKEN", secretToken)
	code, stdout, stderr, err := up(t, "crew-1")
	if code != 0 || err != nil || stdout != "run r-1\n"+srv.URL+"/runs/r-1\n" {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	if len(*seen) != 1 || (*seen)[0].auth != "Bearer "+secretToken {
		t.Fatalf("requests: %+v", *seen)
	}
	// A flag wins over the environment.
	code, _, _, err = up(t, "crew-2", "--token", "other")
	if code != 0 || err != nil || len(*seen) != 2 || (*seen)[1].auth != "Bearer other" || (*seen)[1].path != "/api/crews/crew-2/launch" {
		t.Fatalf("exit %d %v; requests: %+v", code, err, *seen)
	}
}

// A crew ID is one path segment, whatever it holds.
func TestUpEscapesTheCrewID(t *testing.T) {
	clearConductorEnv(t)
	srv, seen := stubServer(t, http.StatusCreated, launchReply)
	if code, _, _, err := up(t, "a/b?c#d", "--server", srv.URL, "--token", secretToken); code != 0 || err != nil {
		t.Fatalf("exit %d %v", code, err)
	}
	if got := (*seen)[0]; got.path != "/api/crews/a%2Fb%3Fc%23d/launch" || got.query != "" {
		t.Fatalf("request: %+v", got)
	}
}

// Without a token the command exits 2 before it sends anything.
func TestUpMissingToken(t *testing.T) {
	clearConductorEnv(t)
	srv, seen := stubServer(t, http.StatusCreated, launchReply)
	code, stdout, _, err := up(t, "crew-1", "--server", srv.URL)
	if code != 2 || err == nil || !strings.Contains(err.Error(), "CONDUCTOR_ADMIN_TOKEN") || stdout != "" {
		t.Fatalf("exit %d %v\nstdout:\n%s", code, err, stdout)
	}
	if len(*seen) != 0 {
		t.Fatalf("a request was sent: %+v", *seen)
	}
}

// A mistake on the command line exits 2 and never repeats the token.
func TestUpUsageErrors(t *testing.T) {
	clearConductorEnv(t)
	srv, seen := stubServer(t, http.StatusCreated, launchReply)
	for name, args := range map[string][]string{
		"no crew":        {"--server", srv.URL, "--token", secretToken},
		"two crews":      {"a", "b", "--server", srv.URL, "--token", secretToken},
		"empty crew":     {"", "--server", srv.URL, "--token", secretToken},
		"unknown flag":   {"a", "--nope", "--token", secretToken},
		"no scheme":      {"a", "--server", "localhost:8080", "--token", secretToken},
		"not http":       {"a", "--server", "ftp://" + secretToken + "@host", "--token", secretToken},
		"no host":        {"a", "--server", "http://", "--token", secretToken},
		"server query":   {"a", "--server", srv.URL + "/?x=1", "--token", secretToken},
		"server section": {"a", "--server", srv.URL + "/#x", "--token", secretToken},
	} {
		code, stdout, stderr, err := up(t, args...)
		if code != 2 || err == nil {
			t.Errorf("%s: exit %d %v", name, code, err)
		}
		noSecret(t, stdout, stderr, err)
	}
	if len(*seen) != 0 {
		t.Fatalf("a request was sent: %+v", *seen)
	}
}

// -h prints the usage and exits 0.
func TestUpHelp(t *testing.T) {
	clearConductorEnv(t)
	code, _, stderr, err := up(t, "-h")
	if code != 0 || err != nil || !strings.Contains(stderr, "conductor up") || !strings.Contains(stderr, "CONDUCTOR_ADMIN_TOKEN") {
		t.Fatalf("exit %d %v\n%s", code, err, stderr)
	}
	// The token's own default is never printed.
	t.Setenv("CONDUCTOR_ADMIN_TOKEN", secretToken)
	_, _, stderr, _ = up(t, "-h")
	noSecret(t, stderr)
}

// The API's error is the command's: code and message, exit 1, nothing on
// stdout.
func TestUpAPIError(t *testing.T) {
	clearConductorEnv(t)
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"envelope":      {http.StatusNotFound, `{"error":{"code":"not_found","message":"no such crew"}}`, "not_found: no such crew"},
		"unauthorized":  {http.StatusUnauthorized, `{"error":{"code":"unauthorized","message":"admin token required"}}`, "unauthorized: admin token required"},
		"not json":      {http.StatusBadGateway, `<html>bad gateway</html>`, "502"},
		"empty":         {http.StatusInternalServerError, ``, "500"},
		"redirect":      {http.StatusFound, ``, "302"},
		"success shape": {http.StatusCreated, `{"run":{"id":""}}`, "no run"},
		"garbled":       {http.StatusCreated, `not json`, "unreadable"},
	} {
		srv, _ := stubServer(t, tc.status, tc.body)
		code, stdout, stderr, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken)
		if code != 1 || err == nil || !strings.Contains(err.Error(), tc.want) || stdout != "" {
			t.Errorf("%s: exit %d %v\nstdout:\n%s\nstderr:\n%s", name, code, err, stdout, stderr)
			continue
		}
		noSecret(t, stdout, stderr, err)
	}
}

// A connection error names the server's host, not the path, the query or the
// token.
func TestUpConnectionError(t *testing.T) {
	clearConductorEnv(t)
	srv, _ := stubServer(t, http.StatusCreated, launchReply)
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	code, stdout, stderr, err := up(t, "crew-1", "--server", "http://user:pw@"+host, "--token", secretToken)
	if code != 1 || err == nil || !strings.Contains(err.Error(), host) || stdout != "" {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	for _, leak := range []string{"/api/crews", "user", "pw", "crew-1"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("%q is in %q", leak, err)
		}
	}
	noSecret(t, stdout, stderr, err)
}

// A server that does not answer in time ends the command; up waits for the
// launch reply.
func TestUpTimeout(t *testing.T) {
	clearConductorEnv(t)
	old := requestTimeout
	requestTimeout = 50 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	code, stdout, _, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "no reply") || stdout != "" {
		t.Fatalf("exit %d %v\nstdout:\n%s", code, err, stdout)
	}
}

// A reply of more than 1 MiB is refused, not buffered.
func TestUpOversizedReply(t *testing.T) {
	clearConductorEnv(t)
	srv, _ := stubServer(t, http.StatusCreated, `{"run":{"id":"r-1"},"pad":"`+strings.Repeat("x", maxReply)+`"}`)
	code, stdout, _, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "larger than") || stdout != "" {
		t.Fatalf("exit %d %v\nstdout:\n%s", code, err, stdout)
	}
}

// The token is sent to the server that was named and to no other: a redirect
// is an error, not followed.
func TestUpDoesNotFollowRedirects(t *testing.T) {
	clearConductorEnv(t)
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { elsewhere.Add(1) }))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/crews/crew-1/launch", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	code, _, _, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken)
	if code != 1 || err == nil || elsewhere.Load() != 0 {
		t.Fatalf("exit %d %v; the redirect target got %d requests", code, err, elsewhere.Load())
	}
}

// --open hands the run's URL to the platform's opener; without an opener the
// URL is still printed and the launch still succeeds.
func TestUpOpen(t *testing.T) {
	clearConductorEnv(t)
	srv, _ := stubServer(t, http.StatusCreated, launchReply)
	var opened []string
	old := openBrowser
	t.Cleanup(func() { openBrowser = old })

	openBrowser = func(url string) error { opened = append(opened, url); return nil }
	want := "run r-1\n" + srv.URL + "/runs/r-1\n"
	code, stdout, stderr, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken, "--open")
	if code != 0 || err != nil || stdout != want || stderr != "" || len(opened) != 1 || opened[0] != srv.URL+"/runs/r-1" {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s\nopened: %v", code, err, stdout, stderr, opened)
	}

	// Not asked for: not opened.
	opened = nil
	if code, _, _, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken); code != 0 || err != nil || len(opened) != 0 {
		t.Fatalf("exit %d %v; opened: %v", code, err, opened)
	}

	// Neither xdg-open nor open: the URL is there to click.
	openBrowser = func(string) error { return errNoOpener }
	code, stdout, stderr, err = up(t, "crew-1", "--server", srv.URL, "--token", secretToken, "--open")
	if code != 0 || err != nil || stdout != want || !strings.Contains(stderr, "xdg-open") {
		t.Fatalf("no opener: exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}

	// An opener that fails is a note, not a failed launch.
	openBrowser = func(string) error { return errors.New("boom") }
	code, stdout, stderr, err = up(t, "crew-1", "--server", srv.URL, "--token", secretToken, "--open")
	if code != 0 || err != nil || stdout != want || !strings.Contains(stderr, "boom") {
		t.Fatalf("failing opener: exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
}

// conductor crews prints one line per crew, in the server's order, and
// tolerates fields it does not know.
func TestCrewsList(t *testing.T) {
	clearConductorEnv(t)
	srv, seen := stubServer(t, http.StatusOK, `{"crews":[
		{"id":"c-1","name":"Ship it","goal":"g","future":{"a":1},"members":[{"name":"lead","agentId":"claude","prompt":"p","start":{"kind":"immediately"}},{"name":"review","agentId":"codex"}]},
		{"id":"c-2","name":"Solo","members":[{"name":"one","agentId":"claude"}]}
	],"more":true}`)
	code, stdout, stderr, err := crews(t, "--server", srv.URL, "--token", secretToken)
	if code != 0 || err != nil {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
	want := "c-1\tShip it\t2 members: lead, review\nc-2\tSolo\t1 members: one\n"
	if stdout != want {
		t.Fatalf("stdout:\n%q\nwant:\n%q", stdout, want)
	}
	if len(*seen) != 1 || (*seen)[0] != (request{http.MethodGet, "/api/crews", "", "Bearer " + secretToken}) {
		t.Fatalf("requests: %+v", *seen)
	}
	noSecret(t, stdout, stderr)
}

func TestCrewsEmpty(t *testing.T) {
	clearConductorEnv(t)
	srv, _ := stubServer(t, http.StatusOK, `{"crews":[]}`)
	code, stdout, stderr, err := crews(t, "--server", srv.URL, "--token", secretToken)
	if code != 0 || err != nil || stdout != "no crews\n" {
		t.Fatalf("exit %d %v\nstdout:\n%q\nstderr:\n%s", code, err, stdout, stderr)
	}
}

func TestCrewsErrors(t *testing.T) {
	clearConductorEnv(t)
	if code, _, _, err := crews(t, "--server", "http://localhost:1"); code != 2 || err == nil || !strings.Contains(err.Error(), "CONDUCTOR_ADMIN_TOKEN") {
		t.Fatalf("no token: exit %d %v", code, err)
	}
	if code, _, _, err := crews(t, "extra", "--token", secretToken); code != 2 || err == nil {
		t.Fatalf("an argument: exit %d %v", code, err)
	}
	srv, _ := stubServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthorized","message":"admin token required"}}`)
	code, stdout, stderr, err := crews(t, "--server", srv.URL, "--token", secretToken)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "unauthorized: admin token required") || stdout != "" {
		t.Fatalf("exit %d %v\nstdout:\n%s", code, err, stdout)
	}
	noSecret(t, stdout, stderr, err)
	srv, _ = stubServer(t, http.StatusOK, `{"crews":`)
	if code, _, _, err := crews(t, "--server", srv.URL, "--token", secretToken); code != 1 || err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("garbled: exit %d %v", code, err)
	}
}

// The two commands are reachable from Run and named in its help.
func TestRunDispatchesUpAndCrews(t *testing.T) {
	clearConductorEnv(t)
	var out, errOut bytes.Buffer
	if code, err := Run(t.Context(), []string{"help"}, nil, &out, &errOut); code != 0 || err != nil {
		t.Fatalf("help: exit %d %v", code, err)
	}
	for _, want := range []string{"conductor up <crew-id>", "conductor crews"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not name %q:\n%s", want, out.String())
		}
	}
	srv, _ := stubServer(t, http.StatusOK, `{"crews":[]}`)
	out.Reset()
	if code, err := Run(t.Context(), []string{"crews", "--server", srv.URL, "--token", secretToken}, nil, &out, &errOut); code != 0 || err != nil || out.String() != "no crews\n" {
		t.Fatalf("crews: exit %d %v\n%s", code, err, out.String())
	}
	if code, err := Run(t.Context(), []string{"up"}, nil, &out, &errOut); code != 2 || err == nil {
		t.Fatalf("up: exit %d %v", code, err)
	}
}
