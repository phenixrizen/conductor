package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
)

const (
	sessionCookie   = "__Host-conductor-session"
	loginCookie     = "__Host-conductor-login"
	loginLifetime   = 5 * time.Minute
	sessionLifetime = time.Hour
)

type browserProvider interface {
	AuthorizationURL(state, nonce, verifier string) (string, error)
	Exchange(context.Context, string, string, string) (authn.Identity, error)
}

type browserStore interface {
	CreateBrowserLogin(context.Context, domain.BrowserLogin) error
	ConsumeBrowserLogin(context.Context, string, string) (domain.BrowserLogin, error)
	CreateBrowserSession(context.Context, domain.BrowserSession) error
	BrowserSession(context.Context, string) (domain.BrowserSession, error)
	RevokeBrowserSession(context.Context, string) error
}

// BrowserConfig pins the public origin independently of untrusted forwarding
// headers. The reverse proxy must preserve Host and serve UI/API on this origin.
type BrowserConfig struct {
	Origin string
	Issuer string
}

type browserAuth struct {
	service              authenticatedService
	provider             browserProvider
	store                browserStore
	origin, host, issuer string
	now                  func() time.Time
}

// BrowserOrigin normalizes the fixed HTTPS origin used for callback, cookies,
// and CSRF checks. Redirect destinations never come from a request parameter.
func BrowserOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", errors.New("browser sign-in requires an HTTPS origin without credentials, path, query, or fragment")
	}
	host := strings.ToLower(u.Host)
	if u.Port() == "443" {
		host = strings.TrimSuffix(host, ":443")
	}
	return "https://" + host, nil
}

// NewBrowserAuthenticated adds browser sessions to the same signed-identity and
// transactional permission boundary used by bearer clients. It never introduces
// a second review command path or accepts client-chosen actor identities.
func NewBrowserAuthenticated(s authenticatedService, verifier tokenVerifier, provider browserProvider, store browserStore, config BrowserConfig) (http.Handler, error) {
	origin, err := BrowserOrigin(config.Origin)
	if err != nil {
		return nil, err
	}
	if provider == nil || store == nil || config.Issuer == "" {
		return nil, errors.New("browser sign-in requires a configured provider and session store")
	}
	u, _ := url.Parse(origin)
	browser := &browserAuth{service: s, provider: provider, store: store, origin: origin, host: u.Host, issuer: config.Issuer, now: time.Now}
	return sharedHandler(s, verifier, browser), nil
}

func authenticationConfig(mode string, browser bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !noQuery(w, r) {
			return
		}
		write(w, http.StatusOK, map[string]any{"mode": mode, "browserLogin": browser})
	}
}

func newBrowserSecret() string {
	var secret [32]byte
	// Go's crypto/rand.Read fills the buffer or terminates on an entropy failure.
	_, _ = rand.Read(secret[:])
	return base64.RawURLEncoding.EncodeToString(secret[:])
}

func secretHash(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func browserSecret(value string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(value) == 43 && len(decoded) == 32
}

func selectedCookie(r *http.Request, name string) (string, bool) {
	values := r.CookiesNamed(name)
	if len(values) != 1 || !browserSecret(values[0].Value) {
		return "", false
	}
	return values[0].Value, true
}

func setBrowserCookie(w http.ResponseWriter, name, value string, lifetime time.Duration) {
	cookie := &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(lifetime.Seconds())}
	if lifetime < 0 {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0).UTC()
	}
	http.SetCookie(w, cookie)
}

func (b *browserAuth) browserRequest(r *http.Request) bool {
	return strings.EqualFold(r.Host, b.host) && len(r.Header.Values("Authorization")) == 0 && len(r.Header.Values("X-Conductor-Actor")) == 0
}

func (b *browserAuth) authenticate(r *http.Request, mutation bool) (domain.BrowserSession, error) {
	if !b.browserRequest(r) {
		return domain.BrowserSession{}, domain.ErrUnauthenticated
	}
	// A cross-origin document cannot use ambient cookies to make commands. The
	// secret is tied to this session, so an old tab cannot act after account change.
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] != b.origin) || (mutation && len(origins) != 1) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return domain.BrowserSession{}, domain.ErrForbidden
	}
	raw, ok := selectedCookie(r, sessionCookie)
	if !ok {
		return domain.BrowserSession{}, domain.ErrUnauthenticated
	}
	session, err := b.store.BrowserSession(r.Context(), secretHash(raw))
	if err != nil {
		return domain.BrowserSession{}, err
	}
	csrf := r.Header.Values("X-Conductor-CSRF")
	if mutation || len(csrf) != 0 {
		if len(csrf) != 1 || !browserSecret(csrf[0]) || subtle.ConstantTimeCompare([]byte(csrf[0]), []byte(session.CSRFToken)) != 1 {
			return domain.BrowserSession{}, domain.ErrForbidden
		}
	}
	return session, nil
}

func (b *browserAuth) login(w http.ResponseWriter, r *http.Request) {
	if !b.browserRequest(r) || r.URL.RawQuery != "" {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	state, binding, nonce, verifier := newBrowserSecret(), newBrowserSecret(), newBrowserSecret(), newBrowserSecret()
	redirect, err := b.provider.AuthorizationURL(state, nonce, verifier)
	if err != nil {
		b.loginFailure(w, http.StatusServiceUnavailable)
		return
	}
	login := domain.BrowserLogin{StateHash: secretHash(state), BindingHash: secretHash(binding), Nonce: nonce, CodeVerifier: verifier, ExpiresAt: b.now().UTC().Add(loginLifetime)}
	if err := b.store.CreateBrowserLogin(r.Context(), login); err != nil {
		b.loginFailure(w, http.StatusServiceUnavailable)
		return
	}
	setBrowserCookie(w, loginCookie, binding, loginLifetime)
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (b *browserAuth) callback(w http.ResponseWriter, r *http.Request) {
	if !b.browserRequest(r) || len(r.URL.RawQuery) > 16<<10 {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	if len(query) > 32 {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	// OAuth requires ignoring unrecognized response parameters. Extensions never
	// become command inputs; retain bounds and reject ambiguous repeated values.
	for _, values := range query {
		if len(values) != 1 || len(values[0]) > 4096 {
			b.loginFailure(w, http.StatusBadRequest)
			return
		}
	}
	binding, ok := selectedCookie(r, loginCookie)
	if !ok || !browserSecret(query.Get("state")) {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	// Consume exactly once before the token request. A timeout or uncertain
	// exchange must start a new login rather than replaying an authorization code.
	login, err := b.store.ConsumeBrowserLogin(r.Context(), secretHash(query.Get("state")), secretHash(binding))
	if err != nil {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	setBrowserCookie(w, loginCookie, "", -1)
	if query.Has("error") || query.Get("code") == "" || (query.Has("iss") && query.Get("iss") != b.issuer) {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	identity, err := b.provider.Exchange(r.Context(), query.Get("code"), login.CodeVerifier, login.Nonce)
	if err != nil || identity.Issuer != b.issuer {
		b.loginFailure(w, http.StatusBadRequest)
		return
	}
	raw, csrf := newBrowserSecret(), newBrowserSecret()
	session := domain.BrowserSession{TokenHash: secretHash(raw), CSRFToken: csrf, Identity: domain.AccessIdentity{Issuer: identity.Issuer, Subject: identity.Subject}, ExpiresAt: b.now().UTC().Add(sessionLifetime)}
	if err := b.store.CreateBrowserSession(r.Context(), session); err != nil {
		b.loginFailure(w, http.StatusForbidden)
		return
	}
	if old, ok := selectedCookie(r, sessionCookie); ok {
		if err := b.store.RevokeBrowserSession(r.Context(), secretHash(old)); err != nil {
			_ = b.store.RevokeBrowserSession(r.Context(), session.TokenHash)
			b.loginFailure(w, http.StatusServiceUnavailable)
			return
		}
	}
	setBrowserCookie(w, sessionCookie, raw, sessionLifetime)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (b *browserAuth) session(w http.ResponseWriter, r *http.Request) {
	if !noQuery(w, r) {
		return
	}
	session, err := b.authenticate(r, false)
	if err != nil {
		fail(w, r, err)
		return
	}
	ctx := domain.WithAccess(r.Context(), domain.AccessRequest{Identity: session.Identity})
	value, err := b.service.Session(ctx)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"session": value, "csrfToken": session.CSRFToken})
}

func (b *browserAuth) logout(w http.ResponseWriter, r *http.Request) {
	if !noQuery(w, r) {
		return
	}
	session, err := b.authenticate(r, true)
	if err != nil {
		fail(w, r, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	var command map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &command) != nil || command == nil || len(command) != 0 {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	if err := b.store.RevokeBrowserSession(r.Context(), session.TokenHash); err != nil {
		fail(w, r, err)
		return
	}
	setBrowserCookie(w, sessionCookie, "", -1)
	write(w, http.StatusOK, map[string]bool{"signedOut": true})
}

func (b *browserAuth) loginFailure(w http.ResponseWriter, status int) {
	// Never echo authorization codes, provider descriptions, token responses, or
	// supplied URLs. The fixed link returns to the workbench without an open redirect.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><title>Sign-in unavailable</title><h1>Sign-in could not be completed</h1><p>Start a new sign-in. If it still fails, ask your Conductor operator to check identity configuration and access.</p><a href=\"/\">Return to Conductor</a></html>")
}
