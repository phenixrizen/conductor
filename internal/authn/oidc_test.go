package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

var testKeys = sync.OnceValues(func() (*rsa.PrivateKey, *rsa.PrivateKey) {
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	b, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return a, b
})

type issuerFixture struct {
	server       *httptest.Server
	mu           sync.Mutex
	keys         []jose.JSONWebKey
	keyBody      string
	metadataBody string
	keyStatus    int
	keyRequests  atomic.Int32
}

func newIssuer(t *testing.T, tls bool) *issuerFixture {
	t.Helper()
	a, _ := testKeys()
	f := &issuerFixture{keys: []jose.JSONWebKey{{Key: &a.PublicKey, KeyID: "key-1", Algorithm: "RS256", Use: "sig"}}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			if f.metadataBody != "" {
				_, _ = w.Write([]byte(f.metadataBody))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.server.URL, "jwks_uri": f.server.URL + "/keys"})
		case "/keys":
			f.keyRequests.Add(1)
			if f.keyStatus != 0 {
				w.WriteHeader(f.keyStatus)
				return
			}
			if f.keyBody != "" {
				_, _ = w.Write([]byte(f.keyBody))
				return
			}
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: f.keys})
		default:
			http.NotFound(w, r)
		}
	})
	if tls {
		f.server = httptest.NewTLSServer(h)
	} else {
		f.server = httptest.NewServer(h)
	}
	t.Cleanup(f.server.Close)
	return f
}

func (f *issuerFixture) config() Config {
	return Config{Issuer: f.server.URL, Audience: "conductor-api", HTTPClient: f.server.Client(), AllowInsecureLoopback: true}
}

func newVerifier(t *testing.T, f *issuerFixture) (*Verifier, time.Time) {
	t.Helper()
	v, err := New(context.Background(), f.config())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	v.now = func() time.Time { return now }
	return v, now
}

func claimsFor(f *issuerFixture, now time.Time) map[string]any {
	return map[string]any{"iss": f.server.URL, "aud": "conductor-api", "sub": "person-1", "exp": now.Add(10 * time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix(), "jti": "token-1", "client_id": "test-client"}
}

func signToken(t *testing.T, key any, alg jose.SignatureAlgorithm, typ, kid string, claims any) string {
	t.Helper()
	options := new(jose.SignerOptions).WithType(jose.ContentType(typ)).WithHeader(jose.HeaderKey("kid"), kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, options)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAccessTokenProfile(t *testing.T) {
	f := newIssuer(t, true)
	v, now := newVerifier(t, f)
	a, b := testKeys()
	tests := []struct {
		name         string
		edit         func(map[string]any)
		typ          string
		badSignature bool
		valid        bool
	}{
		{name: "valid", valid: true},
		{name: "uppercase media type", typ: "at+JWT", valid: true},
		{name: "full media type", typ: "application/at+jwt", valid: true},
		{name: "matching audience in array", edit: func(c map[string]any) { c["aud"] = []string{"other", "conductor-api"} }, valid: true},
		{name: "roles do not become identity", edit: func(c map[string]any) {
			c["roles"] = []string{"admin", "architect"}
			c["email"] = "untrusted@example.test"
		}, valid: true},
		{name: "ID token", typ: "JWT"},
		{name: "wrong issuer", edit: func(c map[string]any) { c["iss"] = f.server.URL + "/" }},
		{name: "wrong audience", edit: func(c map[string]any) { c["aud"] = "conductor-web" }},
		{name: "expired", edit: func(c map[string]any) { c["exp"] = now.Add(-time.Second).Unix() }},
		{name: "expiration is exclusive", edit: func(c map[string]any) { c["exp"] = now.Unix() }},
		{name: "future issued at", edit: func(c map[string]any) { c["iat"] = now.Add(time.Second).Unix() }},
		{name: "not yet valid", edit: func(c map[string]any) { c["nbf"] = now.Add(time.Second).Unix() }},
		{name: "expiry before issue", edit: func(c map[string]any) { c["exp"] = now.Add(-2 * time.Minute).Unix() }},
		{name: "invalid date type", edit: func(c map[string]any) { c["exp"] = "tomorrow" }},
		{name: "invalid audience type", edit: func(c map[string]any) { c["aud"] = []any{"conductor-api", 1} }},
		{name: "blank subject", edit: func(c map[string]any) { c["sub"] = " " }},
		{name: "control in subject", edit: func(c map[string]any) { c["sub"] = "person\u0000" }},
		{name: "bad signature", badSignature: true},
	}
	for _, claim := range []string{"iss", "aud", "exp", "iat", "sub", "client_id", "jti"} {
		tests = append(tests, struct {
			name         string
			edit         func(map[string]any)
			typ          string
			badSignature bool
			valid        bool
		}{name: "missing " + claim, edit: func(c map[string]any) { delete(c, claim) }})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := claimsFor(f, now)
			if tc.edit != nil {
				tc.edit(claims)
			}
			typ := tc.typ
			if typ == "" {
				typ = "at+jwt"
			}
			key := a
			if tc.badSignature {
				key = b
			}
			token := signToken(t, key, jose.RS256, typ, "key-1", claims)
			identity, err := v.Verify(context.Background(), token)
			if tc.valid {
				if err != nil || identity != (Identity{Issuer: f.server.URL, Subject: "person-1"}) {
					t.Fatalf("identity=%+v err=%v", identity, err)
				}
			} else if !errors.Is(err, ErrInvalidToken) || identity != (Identity{}) {
				t.Fatalf("invalid token returned identity=%+v err=%v", identity, err)
			}
		})
	}
	if count := f.keyRequests.Load(); count != 1 {
		t.Fatalf("known keys caused %d fetches", count)
	}
}

func TestRejectMalformedAndUnsupportedTokens(t *testing.T) {
	f := newIssuer(t, false)
	v, now := newVerifier(t, f)
	a, _ := testKeys()
	valid := signToken(t, a, jose.RS256, "at+jwt", "key-1", claimsFor(f, now))
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"at+jwt","kid":"key-1"}`))
	payload, _ := json.Marshal(claimsFor(f, now))
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
	for name, token := range map[string]string{
		"unsigned":            unsigned,
		"HMAC":                signToken(t, []byte(strings.Repeat("x", 32)), jose.HS256, "at+jwt", "key-1", claimsFor(f, now)),
		"RSA wrong algorithm": signToken(t, a, jose.RS512, "at+jwt", "key-1", claimsFor(f, now)),
		"missing type":        signToken(t, a, jose.RS256, "", "key-1", claimsFor(f, now)),
		"missing key ID":      signToken(t, a, jose.RS256, "at+jwt", "", claimsFor(f, now)),
		"empty":               "", "oversized": strings.Repeat("x", MaxTokenBytes+1), "malformed": "a.b.c", "whitespace": valid + "\n", "scheme prefix": "Bearer " + valid,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if count := f.keyRequests.Load(); count != 1 {
		t.Fatalf("invalid tokens caused %d fetches", count)
	}
}

func TestKeyRotationCacheAndRefreshThrottle(t *testing.T) {
	f := newIssuer(t, false)
	v, now := newVerifier(t, f)
	a, b := testKeys()
	old := signToken(t, a, jose.RS256, "at+jwt", "key-1", claimsFor(f, now))
	rotated := signToken(t, b, jose.RS256, "at+jwt", "key-2", claimsFor(f, now))
	f.mu.Lock()
	f.keys = []jose.JSONWebKey{{Key: &b.PublicKey, KeyID: "key-2", Algorithm: "RS256", Use: "sig"}}
	f.mu.Unlock()
	if _, err := v.Verify(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), rotated); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unadvertised cached key accepted: %v", err)
	}
	if count := f.keyRequests.Load(); count != 1 {
		t.Fatalf("refresh throttle ignored: %d", count)
	}
	v.now = func() time.Time { return now.Add(time.Minute) }
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Verify(context.Background(), rotated); err != nil {
				t.Errorf("rotated key: %v", err)
			}
		}()
	}
	wg.Wait()
	if count := f.keyRequests.Load(); count != 2 {
		t.Fatalf("rotation caused %d total fetches", count)
	}
	if _, err := v.Verify(context.Background(), old); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("removed key accepted: %v", err)
	}
	for i := range 12 {
		unknown := signToken(t, a, jose.RS256, "at+jwt", strings.Repeat("x", i+1), claimsFor(f, now))
		if _, err := v.Verify(context.Background(), unknown); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("unknown key accepted: %v", err)
		}
	}
	if count := f.keyRequests.Load(); count != 2 {
		t.Fatalf("unknown key flood caused %d fetches", count)
	}

	// A provider outage cannot prolong the lifetime of cached keys.
	f.mu.Lock()
	f.keyStatus = http.StatusServiceUnavailable
	f.mu.Unlock()
	v.now = func() time.Time { return now.Add(7 * time.Minute) }
	for range 3 {
		if _, err := v.Verify(context.Background(), rotated); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("expired cache accepted: %v", err)
		}
	}
	if count := f.keyRequests.Load(); count != 3 {
		t.Fatalf("failed fetch throttle ignored: %d", count)
	}
}

func TestDiscoveryAndKeyDocumentLimits(t *testing.T) {
	a, _ := testKeys()
	for _, name := range []string{"wrong issuer", "HTTP key endpoint", "oversized metadata", "oversized keys", "duplicate key ID", "empty keys", "too many keys", "private signing key", "malformed JSON", "trailing JSON", "symmetric key"} {
		t.Run(name, func(t *testing.T) {
			f := newIssuer(t, true)
			cfg := f.config()
			cfg.AllowInsecureLoopback = false
			f.mu.Lock()
			switch name {
			case "wrong issuer":
				f.metadataBody = `{"issuer":"https://other.example.test","jwks_uri":"` + f.server.URL + `/keys"}`
			case "HTTP key endpoint":
				f.metadataBody = `{"issuer":"` + f.server.URL + `","jwks_uri":"http://127.0.0.1/keys"}`
			case "oversized metadata":
				f.metadataBody = strings.Repeat(" ", maxDocumentBytes+1)
			case "oversized keys":
				f.keyBody = strings.Repeat(" ", maxDocumentBytes+1)
			case "duplicate key ID":
				f.keys = append(f.keys, f.keys[0])
			case "empty keys":
				f.keys = nil
			case "too many keys":
				f.keyBody = `{"keys":[` + strings.Repeat(`{},`, maxKeys) + `{}]}`
			case "private signing key":
				f.keys[0].Key = a
			case "malformed JSON":
				f.keyBody = "{"
			case "trailing JSON":
				f.keyBody = `{"keys":[]} {}`
			case "symmetric key":
				f.keys[0].Key = []byte(strings.Repeat("x", 32))
			}
			f.mu.Unlock()
			if _, err := New(context.Background(), cfg); err == nil {
				t.Fatal("invalid identity metadata accepted")
			}
		})
	}
}

func TestHTTPSIssuerAndRedirectRules(t *testing.T) {
	f := newIssuer(t, false)
	cfg := f.config()
	cfg.AllowInsecureLoopback = false
	if _, err := New(context.Background(), cfg); err == nil {
		t.Fatal("HTTP issuer accepted without local test opt-in")
	}
	for _, issuer := range []string{"http://example.test", "https://user:password@example.test", "https://example.test?q=1", "https://example.test#fragment", "file:///keys", "https:///missing-host"} {
		if validEndpoint(issuer, true, true) {
			t.Errorf("unsafe issuer accepted: %s", issuer)
		}
	}
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationRequests.Add(1) }))
	t.Cleanup(destination.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	t.Cleanup(redirector.Close)
	custom := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { t.Error("caller redirect policy used"); return nil }}
	if _, err := New(context.Background(), Config{Issuer: redirector.URL, Audience: "api", AllowInsecureLoopback: true, HTTPClient: custom}); err == nil {
		t.Fatal("redirect accepted")
	}
	if destinationRequests.Load() != 0 || custom.Timeout != 0 {
		t.Fatal("redirect followed or caller client mutated")
	}
}

func TestCancellationAndBoundedSkew(t *testing.T) {
	f := newIssuer(t, false)
	v, now := newVerifier(t, f)
	a, _ := testKeys()
	token := signToken(t, a, jose.RS256, "at+jwt", "key-1", claimsFor(f, now))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Verify(ctx, token); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached verify cancellation: %v", err)
	}
	if _, err := New(ctx, f.config()); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery cancellation: %v", err)
	}
	for _, skew := range []time.Duration{-time.Second, time.Minute + time.Nanosecond} {
		cfg := f.config()
		cfg.ClockSkew = skew
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("accepted skew %v", skew)
		}
	}
	v.skew = time.Minute
	claims := claimsFor(f, now)
	claims["iat"] = now.Add(-2 * time.Minute).Unix()
	claims["exp"] = now.Add(-30 * time.Second).Unix()
	if _, err := v.Verify(context.Background(), signToken(t, a, jose.RS256, "at+jwt", "key-1", claims)); err != nil {
		t.Fatalf("bounded skew: %v", err)
	}
	claims["exp"] = now.Add(-time.Minute).Unix()
	if _, err := v.Verify(context.Background(), signToken(t, a, jose.RS256, "at+jwt", "key-1", claims)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("skew boundary: %v", err)
	}
}

func TestUntrustedHeaderCannotSelectKeys(t *testing.T) {
	f := newIssuer(t, false)
	v, now := newVerifier(t, f)
	a, _ := testKeys()
	for _, name := range []jose.HeaderKey{"jku", "x5u", "jwk", "crit", "b64"} {
		t.Run(string(name), func(t *testing.T) {
			var value any = "https://untrusted.example.test/keys"
			switch name {
			case "jwk":
				value = jose.JSONWebKey{Key: &a.PublicKey}
			case "crit":
				value = []string{"unsupported"}
			case "b64":
				value = true
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: a}, new(jose.SignerOptions).WithType("at+jwt").WithHeader("kid", "key-1").WithHeader(name, value))
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(claimsFor(f, now))
			signed, err := signer.Sign(payload)
			if err != nil {
				t.Fatal(err)
			}
			token, err := signed.CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := v.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("untrusted header accepted: %v", err)
			}
		})
	}
	if count := f.keyRequests.Load(); count != 1 {
		t.Fatalf("untrusted headers triggered key refresh: %d", count)
	}
}

func TestSlowDocumentAndRefreshWaitRespectCancellation(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(blocked.Close)
	_, err := New(context.Background(), Config{Issuer: blocked.URL, Audience: "api", AllowInsecureLoopback: true, HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow document was not canceled: %v", err)
	}

	f := newIssuer(t, false)
	v, now := newVerifier(t, f)
	a, _ := testKeys()
	token := signToken(t, a, jose.RS256, "at+jwt", "new-key", claimsFor(f, now))
	started := make(chan struct{})
	refresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(refresh.Close)
	v.jwksURL = refresh.URL
	v.now = func() time.Time { return now.Add(time.Minute) }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	finished := make(chan error, 1)
	go func() { _, err := v.Verify(ctx, token); finished <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if _, err := v.Verify(waitCtx, token); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting request did not cancel: %v", err)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("refresh did not propagate cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not stop")
	}
}
