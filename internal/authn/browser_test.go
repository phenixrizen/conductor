package authn

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const browserClientID = "conductor-browser"
const browserClientSecret = "synthetic-client-secret"
const browserCallback = "https://conductor.example.test/api/v1/auth/callback"
const browserNonce = "synthetic-nonce-with-at-least-32-characters"

type browserIssuer struct {
	server     *httptest.Server
	mu         sync.Mutex
	metadata   map[string]any
	idToken    string
	tokenBody  string
	status     int
	redirect   string
	blockToken bool
	verifier   string
	keys       []jose.JSONWebKey
	tokenCalls atomic.Int32
	keyCalls   atomic.Int32
}

func newBrowserIssuer(t *testing.T) *browserIssuer {
	t.Helper()
	a, _ := testKeys()
	f := &browserIssuer{verifier: GenerateCodeVerifier(), keys: []jose.JSONWebKey{{Key: &a.PublicKey, KeyID: "key-1", Algorithm: "RS256", Use: "sig"}}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(f.metadata)
		case "/keys":
			f.keyCalls.Add(1)
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: f.keys})
		case "/token":
			f.tokenCalls.Add(1)
			id, secret, ok := r.BasicAuth()
			if !ok || id != browserClientID || secret != browserClientSecret || r.URL.RawQuery != "" || r.Method != http.MethodPost {
				t.Error("exchange did not use the configured confidential client and endpoint")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != browserCallback || r.Form.Get("client_secret") != "" || r.Form.Get("client_id") != "" {
				t.Error("unexpected token exchange parameters")
			}
			if f.verifier != r.Form.Get("code_verifier") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			if f.blockToken {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			if f.redirect != "" {
				http.Redirect(w, r, f.redirect, http.StatusTemporaryRedirect)
				return
			}
			if f.status != 0 {
				w.WriteHeader(f.status)
			}
			if f.tokenBody != "" {
				_, _ = w.Write([]byte(f.tokenBody))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-opaque-access-token", "refresh_token": "discarded-refresh-token", "token_type": "Bearer", "id_token": f.idToken})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	f.metadata = map[string]any{"issuer": f.server.URL, "jwks_uri": f.server.URL + "/keys", "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "response_types_supported": []string{"code"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic"}, "code_challenge_methods_supported": []string{"S256"}}
	return f
}

func (f *browserIssuer) config() BrowserConfig {
	return BrowserConfig{Issuer: f.server.URL, ClientID: browserClientID, ClientSecret: browserClientSecret, RedirectURL: browserCallback, HTTPClient: f.server.Client()}
}

func makeBrowser(t *testing.T, f *browserIssuer) (*Browser, time.Time) {
	t.Helper()
	b, err := NewBrowser(context.Background(), f.config())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	b.keys.now = func() time.Time { return now }
	a, _ := testKeys()
	f.idToken = signToken(t, a, jose.RS256, "JWT", "key-1", browserClaims(f, now))
	return b, now
}

func browserClaims(f *browserIssuer, now time.Time) map[string]any {
	return map[string]any{"iss": f.server.URL, "sub": "human-1", "aud": browserClientID, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": browserNonce}
}

func TestBrowserAuthorizationCodePKCEAndIdentity(t *testing.T) {
	f := newBrowserIssuer(t)
	b, _ := makeBrowser(t, f)
	state := "synthetic-state-with-at-least-32-characters"
	location, err := b.AuthorizationURL(state, browserNonce, f.verifier)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	challenge := sha256.Sum256([]byte(f.verifier))
	for key, want := range map[string]string{"response_type": "code", "response_mode": "query", "client_id": browserClientID, "redirect_uri": browserCallback, "scope": "openid", "state": state, "nonce": browserNonce, "code_challenge_method": "S256", "code_challenge": base64.RawURLEncoding.EncodeToString(challenge[:])} {
		if query.Get(key) != want || len(query[key]) != 1 {
			t.Errorf("authorization parameter %s does not match", key)
		}
	}
	if strings.Contains(location, browserClientSecret) || strings.Contains(location, f.verifier) {
		t.Fatal("authorization URL exposed a secret")
	}
	identity, err := b.Exchange(context.Background(), "synthetic-code", f.verifier, browserNonce)
	if err != nil || identity != (Identity{Issuer: f.server.URL, Subject: "human-1"}) {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if f.tokenCalls.Load() != 1 || f.keyCalls.Load() != 1 {
		t.Fatal("exchange made unexpected upstream requests")
	}
	// An ID token accepted for login still cannot authenticate an API request.
	api, err := New(context.Background(), Config{Issuer: f.server.URL, Audience: browserClientID, HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.Verify(context.Background(), f.idToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("ID token crossed API credential boundary: %v", err)
	}
}

func TestBrowserIDTokenProfile(t *testing.T) {
	a, badKey := testKeys()
	for _, tc := range []struct {
		name         string
		change       func(map[string]any)
		typ          string
		badSignature bool
		valid        bool
	}{
		{name: "valid roles grant no authority", change: func(c map[string]any) { c["roles"] = []string{"admin", "architect"} }, valid: true},
		{name: "matching authorized party", change: func(c map[string]any) { c["azp"] = browserClientID }, valid: true},
		{name: "full JWT type", typ: "application/jwt", valid: true},
		{name: "optional JWT type absent", typ: "omitted", valid: true},
		{name: "matching access token hash", change: func(c map[string]any) {
			digest := sha256.Sum256([]byte("synthetic-opaque-access-token"))
			c["at_hash"] = base64.RawURLEncoding.EncodeToString(digest[:16])
		}, valid: true},
		{name: "wrong issuer", change: func(c map[string]any) { c["iss"] = "https://other.example.test" }},
		{name: "API audience", change: func(c map[string]any) { c["aud"] = "conductor-api" }},
		{name: "untrusted additional audience", change: func(c map[string]any) { c["aud"] = []string{browserClientID, "other"}; c["azp"] = browserClientID }},
		{name: "wrong authorized party", change: func(c map[string]any) { c["azp"] = "other" }},
		{name: "null authorized party", change: func(c map[string]any) { c["azp"] = nil }},
		{name: "wrong nonce", change: func(c map[string]any) { c["nonce"] = "different-nonce-with-at-least-32-characters" }},
		{name: "missing nonce", change: func(c map[string]any) { delete(c, "nonce") }},
		{name: "missing subject", change: func(c map[string]any) { delete(c, "sub") }},
		{name: "missing issued at", change: func(c map[string]any) { delete(c, "iat") }},
		{name: "missing expiration", change: func(c map[string]any) { delete(c, "exp") }},
		{name: "expired", change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "exclusive expiration boundary", change: func(c map[string]any) { c["exp"] = time.Now().UTC().Truncate(time.Second).Unix() }},
		{name: "future issued at", change: func(c map[string]any) { c["iat"] = time.Now().Add(time.Minute).Unix() }},
		{name: "future nbf within library five-minute allowance", change: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Minute).Unix() }},
		{name: "malformed date", change: func(c map[string]any) { c["iat"] = "not-a-date" }},
		{name: "bad signature", badSignature: true},
		{name: "access token type", typ: "at+jwt"},
		{name: "mismatched access token hash", change: func(c map[string]any) { c["at_hash"] = "wrong-hash" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBrowserIssuer(t)
			browser, now := makeBrowser(t, f)
			claims := browserClaims(f, now)
			if tc.change != nil {
				tc.change(claims)
			}
			if tc.name == "exclusive expiration boundary" {
				claims["exp"] = now.Unix()
			}
			typ := tc.typ
			if typ == "" {
				typ = "JWT"
			}
			key := a
			if tc.badSignature {
				key = badKey
			}
			f.idToken = signToken(t, key, jose.RS256, typ, "key-1", claims)
			if typ == "omitted" {
				signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, new(jose.SignerOptions).WithHeader("kid", "key-1"))
				if err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(claims)
				signed, err := signer.Sign(payload)
				if err != nil {
					t.Fatal(err)
				}
				f.idToken, err = signed.CompactSerialize()
				if err != nil {
					t.Fatal(err)
				}
			}
			identity, err := browser.Exchange(context.Background(), "synthetic-code", f.verifier, browserNonce)
			if tc.valid {
				if err != nil || identity.Subject != "human-1" {
					t.Fatalf("valid identity: %+v %v", identity, err)
				}
			} else if !errors.Is(err, ErrInvalidLogin) || identity != (Identity{}) {
				t.Fatalf("invalid identity: %+v %v", identity, err)
			}
		})
	}
}

func TestBrowserExchangeFailsWithoutRetryOrSecretDiagnostics(t *testing.T) {
	for _, name := range []string{"PKCE mismatch", "token error", "oversized response", "malformed JSON", "missing ID token", "oversized ID token", "unsigned ID token", "HS256 ID token", "RS512 ID token", "untrusted key header"} {
		t.Run(name, func(t *testing.T) {
			f := newBrowserIssuer(t)
			b, now := makeBrowser(t, f)
			a, _ := testKeys()
			verifier := f.verifier
			switch name {
			case "PKCE mismatch":
				verifier = GenerateCodeVerifier()
			case "token error":
				f.status = http.StatusBadRequest
				f.tokenBody = `{"error":"invalid_grant","error_description":"` + browserClientSecret + ` synthetic-code"}`
			case "oversized response":
				f.tokenBody = strings.Repeat(" ", maxDocumentBytes+1)
			case "malformed JSON":
				f.tokenBody = `{"id_token":"` + browserClientSecret
			case "missing ID token":
				f.tokenBody = `{"access_token":"opaque","token_type":"Bearer"}`
			case "oversized ID token":
				f.idToken = strings.Repeat("x", MaxTokenBytes+1)
			case "unsigned ID token":
				f.idToken = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + ".e30."
			case "HS256 ID token":
				f.idToken = signToken(t, []byte(strings.Repeat("x", 32)), jose.HS256, "JWT", "key-1", browserClaims(f, now))
			case "RS512 ID token":
				f.idToken = signToken(t, a, jose.RS512, "JWT", "key-1", browserClaims(f, now))
			case "untrusted key header":
				signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: a}, new(jose.SignerOptions).WithType("JWT").WithHeader("kid", "key-1").WithHeader("jku", "https://attacker.example.test/keys"))
				if err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(browserClaims(f, now))
				signed, err := signer.Sign(payload)
				if err != nil {
					t.Fatal(err)
				}
				f.idToken, err = signed.CompactSerialize()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := b.Exchange(context.Background(), "synthetic-code", verifier, browserNonce)
			if !errors.Is(err, ErrInvalidLogin) || strings.Contains(err.Error(), browserClientSecret) || strings.Contains(err.Error(), "synthetic-code") {
				t.Fatalf("exchange error was not sanitized: %v", err)
			}
			if f.tokenCalls.Load() != 1 {
				t.Fatalf("one-use code was retried %d times", f.tokenCalls.Load())
			}
		})
	}
}

func TestBrowserConfigurationAndProtocolBounds(t *testing.T) {
	for _, name := range []string{"HTTP issuer", "HTTP callback", "callback userinfo", "callback query", "callback fragment", "empty client secret", "HTTP token endpoint", "HTTP authorization endpoint", "preselected authorization parameters", "missing code support", "missing RS256 support", "unsupported authentication method", "unsupported PKCE method", "default authentication method", "unadvertised PKCE"} {
		t.Run(name, func(t *testing.T) {
			f := newBrowserIssuer(t)
			cfg := f.config()
			valid := false
			switch name {
			case "HTTP issuer":
				cfg.Issuer = "http://127.0.0.1"
			case "HTTP callback":
				cfg.RedirectURL = "http://127.0.0.1/callback"
			case "callback userinfo":
				cfg.RedirectURL = "https://user:secret@conductor.example.test/callback"
			case "callback query":
				cfg.RedirectURL = browserCallback + "?returnTo=elsewhere"
			case "callback fragment":
				cfg.RedirectURL = browserCallback + "#"
			case "empty client secret":
				cfg.ClientSecret = ""
			case "HTTP token endpoint":
				f.metadata["token_endpoint"] = "http://127.0.0.1/token"
			case "HTTP authorization endpoint":
				f.metadata["authorization_endpoint"] = "http://127.0.0.1/authorize"
			case "preselected authorization parameters":
				f.metadata["authorization_endpoint"] = f.server.URL + "/authorize?scope=extra-permissions"
			case "missing code support":
				f.metadata["response_types_supported"] = []string{"id_token"}
			case "missing RS256 support":
				f.metadata["id_token_signing_alg_values_supported"] = []string{"HS256"}
			case "unsupported authentication method":
				f.metadata["token_endpoint_auth_methods_supported"] = []string{"client_secret_post"}
			case "unsupported PKCE method":
				f.metadata["code_challenge_methods_supported"] = []string{"plain"}
			case "default authentication method":
				delete(f.metadata, "token_endpoint_auth_methods_supported")
				valid = true
			case "unadvertised PKCE":
				delete(f.metadata, "code_challenge_methods_supported")
				valid = true
			}
			if _, err := NewBrowser(context.Background(), cfg); (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
			if f.tokenCalls.Load() != 0 {
				t.Fatal("configuration sent credentials")
			}
		})
	}
	f := newBrowserIssuer(t)
	b, _ := makeBrowser(t, f)
	for _, bad := range []string{"", "short", strings.Repeat("x", 257), "line\nbreak"} {
		if _, err := b.AuthorizationURL(bad, browserNonce, f.verifier); !errors.Is(err, ErrInvalidLogin) {
			t.Fatal("invalid state accepted")
		}
		if _, err := b.AuthorizationURL(browserNonce, bad, f.verifier); !errors.Is(err, ErrInvalidLogin) {
			t.Fatal("invalid nonce accepted")
		}
		if _, err := b.Exchange(context.Background(), "synthetic-code", f.verifier, bad); !errors.Is(err, ErrInvalidLogin) {
			t.Fatal("invalid expected nonce accepted")
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 42), strings.Repeat("x", 129), strings.Repeat("x", 43) + "="} {
		if _, err := b.AuthorizationURL(browserNonce, browserNonce, bad); !errors.Is(err, ErrInvalidLogin) {
			t.Fatal("invalid verifier accepted")
		}
		if _, err := b.Exchange(context.Background(), "synthetic-code", bad, browserNonce); !errors.Is(err, ErrInvalidLogin) {
			t.Fatal("invalid exchange verifier accepted")
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 8193), "code\r\n"} {
		if _, err := b.Exchange(context.Background(), bad, f.verifier, browserNonce); !errors.Is(err, ErrInvalidLogin) {
			t.Fatal("invalid code accepted")
		}
	}
	if f.tokenCalls.Load() != 0 {
		t.Fatal("invalid protocol inputs reached token endpoint")
	}
}

func TestBrowserRejectsRedirectAndPropagatesCancellation(t *testing.T) {
	f := newBrowserIssuer(t)
	b, _ := makeBrowser(t, f)
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	f.redirect = destination.URL
	if _, err := b.Exchange(context.Background(), "synthetic-code", f.verifier, browserNonce); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("redirect error: %v", err)
	}
	if forwarded.Load() != 0 || f.tokenCalls.Load() != 1 {
		t.Fatal("token exchange redirected credentials or retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Exchange(ctx, "synthetic-code", f.verifier, browserNonce); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if f.tokenCalls.Load() != 1 {
		t.Fatal("canceled request performed an exchange")
	}
}

func TestBrowserKeyRotationUsesBoundedSharedCache(t *testing.T) {
	f := newBrowserIssuer(t)
	b, now := makeBrowser(t, f)
	_, rotated := testKeys()
	f.keys = []jose.JSONWebKey{{Key: &rotated.PublicKey, KeyID: "key-2", Algorithm: "RS256", Use: "sig"}}
	f.idToken = signToken(t, rotated, jose.RS256, "JWT", "key-2", browserClaims(f, now))
	if _, err := b.Exchange(context.Background(), "synthetic-code", f.verifier, browserNonce); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("unknown key bypassed refresh throttle: %v", err)
	}
	b.keys.now = func() time.Time { return now.Add(time.Minute) }
	identity, err := b.Exchange(context.Background(), "new-synthetic-code", f.verifier, browserNonce)
	if err != nil || identity.Subject != "human-1" {
		t.Fatalf("rotated identity: %+v %v", identity, err)
	}
	if f.keyCalls.Load() != 2 {
		t.Fatalf("rotation key requests: %d", f.keyCalls.Load())
	}
}

func TestBrowserTokenResponseCancellation(t *testing.T) {
	f := newBrowserIssuer(t)
	b, _ := makeBrowser(t, f)
	f.blockToken = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := b.Exchange(ctx, "synthetic-code", f.verifier, browserNonce); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("token response did not preserve cancellation: %v", err)
	}
}
