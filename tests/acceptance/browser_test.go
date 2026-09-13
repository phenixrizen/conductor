package acceptance_test

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

const browserClientID = "synthetic-browser-client"
const browserClientSecret = "synthetic-browser-client-secret"
const browserSessionCookie = "__Host-conductor-session"
const browserLoginCookie = "__Host-conductor-login"

type browserCode struct {
	subject, nonce, challenge, redirect string
	expires                             time.Time
}
type browserIssuer struct {
	*accessIssuer
	server        *httptest.Server
	client        *http.Client
	callback      string
	mu            sync.Mutex
	codes         map[string]browserCode
	tokenRequests atomic.Int32
}

func newBrowserIssuer(t *testing.T, callback string) *browserIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Distinct hosts model a real identity provider without sending Conductor's
	// host-only cookies to the issuer merely because both test servers are local.
	issuer := &browserIssuer{accessIssuer: &accessIssuer{url: "https://localhost:" + port, key: key}, server: server, callback: callback, codes: make(map[string]browserCode)}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.url, "jwks_uri": issuer.url + "/keys", "authorization_endpoint": issuer.url + "/authorize", "token_endpoint": issuer.url + "/token", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic"}, "code_challenge_methods_supported": []string{"S256"}})
		case "/keys":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "synthetic-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
		case "/authorize":
			issuer.authorize(w, r)
		case "/token":
			issuer.exchange(t, w, r)
		default:
			http.NotFound(w, r)
		}
	})
	server.StartTLS()
	t.Cleanup(server.Close)
	issuer.client = server.Client()
	transport := issuer.client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "127.0.0.1" // httptest's trusted certificate SAN
	issuer.client.Transport = transport
	issuer.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return issuer
}

func (p *browserIssuer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if r.Method != http.MethodGet || q.Get("client_id") != browserClientID || q.Get("redirect_uri") != p.callback || q.Get("response_type") != "code" || q.Get("scope") != "openid" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("nonce") == "" {
		http.Error(w, "invalid synthetic authorization request", 400)
		return
	}
	identity := q.Get("identity")
	if identity == "" {
		// This account picker exists only in the synthetic test issuer, never in
		// Conductor. A real issuer owns authentication and account selection.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><title>Synthetic identity issuer</title><h1>Synthetic identity issuer</h1><form method=\"get\" action=\"/authorize\">")
		for key, values := range q {
			for _, value := range values {
				_, _ = fmt.Fprintf(w, "<input type=\"hidden\" name=\"%s\" value=\"%s\">", template.HTMLEscapeString(key), template.HTMLEscapeString(value))
			}
		}
		for _, name := range []string{"author", "reviewer", "reader", "agent", "other", "unregistered"} {
			_, _ = fmt.Fprintf(w, "<button name=\"identity\" value=\"%s\">Sign in as %s</button>", name, name)
		}
		_, _ = io.WriteString(w, "</form></html>")
		return
	}
	allowed := map[string]bool{"author": true, "reviewer": true, "reader": true, "agent": true, "other": true, "unregistered": true}
	if !allowed[identity] {
		http.Error(w, "unknown synthetic identity", 400)
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		http.Error(w, "entropy unavailable", 503)
		return
	}
	code := base64.RawURLEncoding.EncodeToString(random[:])
	p.mu.Lock()
	p.codes[code] = browserCode{subject: "subject-" + identity, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: p.callback, expires: time.Now().Add(time.Minute)}
	p.mu.Unlock()
	target := p.callback + "?" + url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {p.url}, "scope": {"openid"}}.Encode()
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (p *browserIssuer) exchange(t *testing.T, w http.ResponseWriter, r *http.Request) {
	p.tokenRequests.Add(1)
	client, secret, ok := r.BasicAuth()
	if r.Method != http.MethodPost || !ok || client != browserClientID || secret != browserClientSecret || r.ParseForm() != nil {
		http.Error(w, "invalid synthetic client", 401)
		return
	}
	p.mu.Lock()
	code, found := p.codes[r.Form.Get("code")]
	delete(p.codes, r.Form.Get("code"))
	p.mu.Unlock()
	challenge := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !found || time.Now().After(code.expires) || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != code.redirect || base64.RawURLEncoding.EncodeToString(challenge[:]) != code.challenge {
		http.Error(w, "invalid synthetic authorization code", 400)
		return
	}
	now := time.Now()
	claims := map[string]any{"iss": p.url, "sub": code.subject, "aud": browserClientID, "nonce": code.nonce, "iat": now.Add(-time.Second).Unix(), "exp": now.Add(5 * time.Minute).Unix(), "azp": browserClientID, "roles": []string{"administrator", "architect"}, "kind": "human"}
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "synthetic-key"})
	body, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Error(err)
		http.Error(w, "signing failed", 500)
		return
	}
	token := signed + "." + base64.RawURLEncoding.EncodeToString(signature)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": token, "access_token": "synthetic-unused-provider-access-token", "token_type": "Bearer", "expires_in": 300})
}

type browserFixture struct {
	*accessFixture
	app      *httptest.Server
	provider *browserIssuer
}

func newBrowserFixture(t *testing.T, withUI bool) *browserFixture {
	return newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: withUI})
}

type browserFixtureOptions struct {
	withUI, collections, coordination, deliveries bool
	wrap                                          func(http.Handler) http.Handler
}

func newConfiguredBrowserFixture(t *testing.T, options browserFixtureOptions) *browserFixture {
	t.Helper()
	app := httptest.NewUnstartedServer(nil)
	t.Cleanup(app.Close)
	origin := "https://" + app.Listener.Addr().String()
	issuer := newBrowserIssuer(t, origin+"/api/v1/auth/callback")
	timeout := 60 * time.Second
	if options.withUI {
		timeout = 2 * time.Minute
	}
	f := newAccessFixtureWithIssuer(t, issuer.accessIssuer, issuer.client, timeout)
	browser, err := authn.NewBrowser(f.ctx, authn.BrowserConfig{Issuer: issuer.url, ClientID: browserClientID, ClientSecret: browserClientSecret, RedirectURL: issuer.callback, HTTPClient: issuer.client})
	if err != nil {
		t.Fatal(err)
	}
	shared := service.NewAuthenticated(f.db)
	if options.collections {
		shared = shared.WithCollections()
	}
	if options.coordination {
		shared = shared.WithCoordination()
	}
	if options.deliveries {
		shared = shared.WithDeliveries()
	}
	handler, err := api.NewBrowserAuthenticated(shared, f.verifier, browser, f.db, api.BrowserConfig{Origin: origin, Issuer: issuer.url})
	if err != nil {
		t.Fatal(err)
	}
	if options.wrap != nil {
		handler = options.wrap(handler)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	if options.withUI {
		dist, err := filepath.Abs("../../apps/web/dist")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
			t.Fatal("browser acceptance requires npm --prefix apps/web run build")
		}
		mux.Handle("/", http.FileServer(http.Dir(dist)))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "synthetic workbench") })
	}
	app.Config.Handler = mux
	app.StartTLS()
	return &browserFixture{accessFixture: f, app: app, provider: issuer}
}
func (f *browserFixture) browserClient() *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		f.t.Fatal(err)
	}
	client := *f.app.Client()
	client.Jar = jar
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
func (f *browserFixture) call(client *http.Client, method, path string, body any, headers http.Header, want int, result any) *http.Response {
	f.t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(f.ctx, method, f.app.URL+path, bytes.NewReader(encoded))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		req.Header[key] = append([]string(nil), values...)
	}
	response, err := client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	response.Body.Close()
	if err != nil || len(data) > 2<<20 {
		f.t.Fatalf("bounded browser response: bytes=%d err=%v", len(data), err)
	}
	if response.StatusCode != want {
		f.t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, want, data)
	}
	if strings.Contains(string(data), browserClientSecret) || strings.Contains(string(data), "synthetic-unused-provider-access-token") {
		f.t.Fatal("browser response exposed provider credential")
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			f.t.Fatalf("decode browser response: %v", err)
		}
	}
	return response
}
func (f *browserFixture) beginLogin(client *http.Client) (string, *http.Cookie) {
	response := f.call(client, http.MethodGet, "/api/v1/auth/login", nil, nil, http.StatusSeeOther, nil)
	var binding *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == browserLoginCookie {
			binding = cookie
		}
	}
	if binding == nil || !binding.Secure || !binding.HttpOnly || binding.Path != "/" || binding.Domain != "" || binding.SameSite != http.SameSiteLaxMode {
		f.t.Fatal("login binding cookie lacks host-only browser protections")
	}
	return response.Header.Get("Location"), binding
}
func (f *browserFixture) chooseIdentity(authorization, identity string) string {
	target, err := url.Parse(authorization)
	if err != nil {
		f.t.Fatal(err)
	}
	q := target.Query()
	q.Set("identity", identity)
	target.RawQuery = q.Encode()
	request, err := http.NewRequestWithContext(f.ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	response, err := f.provider.client.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		f.t.Fatalf("synthetic identity choice: %d", response.StatusCode)
	}
	callback := response.Header.Get("Location")
	if !strings.HasPrefix(callback, f.app.URL+"/api/v1/auth/callback?") {
		f.t.Fatal("unexpected callback origin")
	}
	return strings.TrimPrefix(callback, f.app.URL)
}
func (f *browserFixture) login(client *http.Client, identity string) string {
	authorization, _ := f.beginLogin(client)
	callback := f.chooseIdentity(authorization, identity)
	response := f.call(client, http.MethodGet, callback, nil, nil, http.StatusSeeOther, nil)
	found := false
	for _, cookie := range response.Cookies() {
		if cookie.Name == browserSessionCookie {
			found = true
			if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 3600 {
				f.t.Fatal("session cookie lacks browser protections")
			}
		}
	}
	if !found {
		f.t.Fatal("callback did not set session cookie")
	}
	return f.csrf(client, "person-"+identity)
}
func (f *browserFixture) csrf(client *http.Client, principal string) string {
	var session struct {
		Session   domain.Session `json:"session"`
		CSRFToken string         `json:"csrfToken"`
	}
	f.call(client, http.MethodGet, "/api/v1/auth/session", nil, nil, http.StatusOK, &session)
	if session.Session.Principal.ID != principal || len(session.CSRFToken) != 43 {
		f.t.Fatalf("browser identity/CSRF response: %+v", session)
	}
	return session.CSRFToken
}
func (f *browserFixture) headers(csrf, workspace, repository string) http.Header {
	headers := make(http.Header)
	headers.Set("Origin", f.app.URL)
	headers.Set("X-Conductor-CSRF", csrf)
	headers.Set("X-Conductor-Workspace", workspace)
	headers.Set("X-Conductor-Repository", repository)
	return headers
}

func TestBrowserCodeFlowBindingCSRFAndExactReview(t *testing.T) {
	f := newBrowserFixture(t, false)
	reviewer := f.browserClient()
	otherBrowser := f.browserClient()
	authorization, binding := f.beginLogin(reviewer)
	callback := f.chooseIdentity(authorization, "reviewer")
	wrongState, _ := url.Parse(callback)
	query := wrongState.Query()
	query.Set("state", strings.Repeat("A", 43))
	wrongState.RawQuery = query.Encode()
	f.call(reviewer, http.MethodGet, wrongState.String(), nil, nil, http.StatusBadRequest, nil)
	_, _ = f.beginLogin(otherBrowser)
	f.call(otherBrowser, http.MethodGet, callback, nil, nil, http.StatusBadRequest, nil)
	f.call(reviewer, http.MethodGet, callback, nil, nil, http.StatusSeeOther, nil)
	csrf := f.csrf(reviewer, "person-reviewer")
	// Restoring the original binding cookie cannot replay a consumed challenge.
	target, _ := url.Parse(f.app.URL)
	reviewer.Jar.SetCookies(target, []*http.Cookie{binding})
	requests := f.provider.tokenRequests.Load()
	f.call(reviewer, http.MethodGet, callback, nil, nil, http.StatusBadRequest, nil)
	if f.provider.tokenRequests.Load() != requests {
		t.Fatal("replayed callback retried the token exchange")
	}
	created := f.create("author", "team", "application", domain.Content{"intent": "browser exact review"})
	path := "/api/v1/changes/" + created.ID
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	headers := f.headers(csrf, "team", "application")
	f.call(reviewer, http.MethodGet, path, nil, headers, 200, nil)
	approval := map[string]any{"revision": 1, "digest": created.Revision.Digest}
	for _, alter := range []func(http.Header){
		func(h http.Header) { h.Del("Origin") }, func(h http.Header) { h.Set("Origin", "https://other.example.test") }, func(h http.Header) { h.Set("X-Conductor-CSRF", strings.Repeat("A", 43)) }, func(h http.Header) { h.Del("X-Conductor-CSRF") }, func(h http.Header) { h.Add("Origin", f.app.URL) },
	} {
		changed := headers.Clone()
		alter(changed)
		f.call(reviewer, http.MethodPost, path+"/approvals", approval, changed, http.StatusForbidden, nil)
	}
	mixed := headers.Clone()
	mixed.Set("Authorization", "Bearer "+f.tokens["reviewer"])
	f.call(reviewer, http.MethodGet, path, nil, mixed, http.StatusUnauthorized, nil)
	mixed = headers.Clone()
	mixed.Set("X-Conductor-Actor", "person-author")
	f.call(reviewer, http.MethodGet, path, nil, mixed, http.StatusUnauthorized, nil)
	private := f.create("author", "team", "private", domain.Content{"intent": "private browser package"})
	for _, endpoint := range accessPackageEndpoints(private) {
		f.call(reviewer, endpoint.method, endpoint.path, endpoint.body, headers, http.StatusNotFound, nil)
	}
	// Another engineer advances the package while this browser retains revision 1.
	f.request("author", "team", "application", http.MethodPost, path+"/revisions", map[string]any{"expectedRevision": 1, "content": domain.Content{"intent": "new inspected revision"}}, 201, nil)
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 2}, 200, nil)
	f.call(reviewer, http.MethodPost, path+"/approvals", approval, headers, http.StatusConflict, nil)
	var current domain.Package
	f.call(reviewer, http.MethodGet, path, nil, headers, 200, &current)
	f.call(reviewer, http.MethodPost, path+"/approvals", map[string]any{"revision": 2, "digest": current.Revision.Digest}, headers, 201, &current)
	if !current.Approved || current.Approval == nil || current.Approval.Reviewer != "person-reviewer" {
		t.Fatal("browser approval lost independent server identity")
	}
	// Database revocation applies to an existing cookie without logging out or
	// refreshing provider identity; permitted inspection remains available.
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: true}}}); err != nil {
		t.Fatal(err)
	}
	f.call(reviewer, http.MethodPost, path+"/approvals", map[string]any{"revision": 2, "digest": current.Revision.Digest}, headers, http.StatusForbidden, nil)
	f.call(reviewer, http.MethodGet, path, nil, headers, http.StatusOK, nil)
	foreign := f.create("other", "other-team", "other-application", domain.Content{"intent": "different browser workspace"})
	f.call(reviewer, http.MethodGet, "/api/v1/changes/"+foreign.ID, nil, headers, http.StatusNotFound, nil)
	oldCSRF := csrf
	csrf = f.login(reviewer, "author")
	f.call(reviewer, http.MethodGet, path, nil, f.headers(oldCSRF, "team", "application"), http.StatusForbidden, nil)
	f.call(reviewer, http.MethodPost, path+"/approvals", map[string]any{"revision": 2, "digest": current.Revision.Digest}, f.headers(oldCSRF, "team", "application"), http.StatusForbidden, nil)
	f.call(reviewer, http.MethodPost, path+"/approvals", map[string]any{"revision": 2, "digest": current.Revision.Digest}, f.headers(csrf, "team", "application"), http.StatusUnprocessableEntity, nil)
}

func TestBrowserSessionLogoutExpiryAndEligibility(t *testing.T) {
	f := newBrowserFixture(t, false)
	browser := f.browserClient()
	csrf := f.login(browser, "reviewer")
	target, _ := url.Parse(f.app.URL)
	cookies := browser.Jar.Cookies(target)
	f.call(browser, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, f.headers(csrf, "", ""), 200, nil)
	f.call(browser, http.MethodGet, "/api/v1/auth/session", nil, nil, 401, nil)
	browser.Jar.SetCookies(target, cookies)
	f.call(browser, http.MethodGet, "/api/v1/auth/session", nil, nil, 401, nil)
	f.login(browser, "reviewer")
	if _, err := f.sql.Exec(f.ctx, `UPDATE browser_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	f.call(browser, http.MethodGet, "/api/v1/auth/session", nil, nil, 401, nil)
	f.login(browser, "reviewer")
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "person-reviewer", Issuer: f.provider.url, Subject: "subject-reviewer", Kind: "human", Active: false}}}); err != nil {
		t.Fatal(err)
	}
	f.call(browser, http.MethodGet, "/api/v1/auth/session", nil, nil, 401, nil)
	for _, identity := range []string{"agent", "unregistered"} {
		c := f.browserClient()
		authorization, _ := f.beginLogin(c)
		callback := f.chooseIdentity(authorization, identity)
		f.call(c, http.MethodGet, callback, nil, nil, http.StatusForbidden, nil)
		f.call(c, http.MethodGet, "/api/v1/auth/session", nil, nil, 401, nil)
	}
}

func TestAuthenticatedBrowserWorkbench(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 to run actual authenticated browser acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newBrowserFixture(t, true)
	// Give this synthetic reviewer two scopes to exercise interface clearing.
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Workspaces: []domain.WorkspaceConfig{{ID: "empty-team", Name: "Synthetic empty workspace"}}, Memberships: []domain.MembershipConfig{{WorkspaceID: "empty-team", PrincipalID: "person-reviewer", Active: true}}, Grants: []domain.GrantConfig{{RepositoryID: "private", PrincipalID: "person-reviewer", CanRead: true}}}); err != nil {
		t.Fatal(err)
	}
	created := f.create("author", "team", "application", domain.Content{"intent": map[string]any{"title": "Authenticated shared review acceptance"}, "futureField": map[string]any{"retained": true}})
	f.request("author", "team", "application", http.MethodPost, "/api/v1/changes/"+created.ID+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/authenticated.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_CHANGE_ID="+created.ID, "CONDUCTOR_BROWSER_AUTHOR_TOKEN="+f.tokens["author"], "CONDUCTOR_BROWSER_SCREENSHOT=/tmp/conductor-authenticated-workbench.png")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("authenticated browser acceptance failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
