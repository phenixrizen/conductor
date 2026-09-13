// Package authn verifies identity only. Workspace membership and review authority
// come from Conductor's database, never token roles, groups, or email addresses.
package authn

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	MaxTokenBytes    = 16 << 10
	maxDocumentBytes = 256 << 10
	maxKeys          = 64
	requestTimeout   = 5 * time.Second
	keyCacheTTL      = 5 * time.Minute
	refreshInterval  = 30 * time.Second
)

// ErrInvalidToken intentionally omits token contents and provider details.
var ErrInvalidToken = errors.New("invalid access token")

type Identity struct {
	Issuer  string
	Subject string
}

type Config struct {
	Issuer   string
	Audience string
	// ClockSkew defaults to zero and may not exceed one minute.
	ClockSkew time.Duration
	// HTTPClient permits a custom trust store. Timeouts and redirect rejection
	// remain enforced on a copy, without changing the caller's client.
	HTTPClient *http.Client
	// AllowInsecureLoopback permits HTTP to literal loopback IPs for local tests.
	// Production configuration must leave it false.
	AllowInsecureLoopback bool
}

type Verifier struct {
	issuer   string
	audience string
	skew     time.Duration
	client   *http.Client
	jwksURL  string
	now      func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	expires     time.Time
	nextRefresh time.Time
	refreshing  chan struct{}
}

// New discovers the configured issuer and fetches its keys before accepting any
// traffic. OIDC discovery is used only for metadata; these are RFC 9068 access
// tokens, not OIDC ID tokens. Only compact, RS256-signed tokens are supported.
func New(ctx context.Context, cfg Config) (*Verifier, error) {
	if !validEndpoint(cfg.Issuer, cfg.AllowInsecureLoopback, true) || !validIdentifier(cfg.Audience, 2048) {
		return nil, errors.New("authentication requires an HTTPS issuer and a nonempty audience")
	}
	if cfg.ClockSkew < 0 || cfg.ClockSkew > time.Minute {
		return nil, errors.New("authentication clock skew must be between zero and one minute")
	}
	client := http.Client{Timeout: requestTimeout}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
		if client.Timeout <= 0 || client.Timeout > requestTimeout {
			client.Timeout = requestTimeout
		}
	}
	// Discovery and public signing keys need no browser cookies.
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	v := &Verifier{issuer: cfg.Issuer, audience: cfg.Audience, skew: cfg.ClockSkew, client: &client, now: time.Now}
	var metadata struct {
		Issuer  string `json:"issuer"`
		JWKSURL string `json:"jwks_uri"`
	}
	if err := v.getJSON(ctx, strings.TrimSuffix(cfg.Issuer, "/")+"/.well-known/openid-configuration", &metadata); err != nil {
		return nil, fmt.Errorf("discover identity issuer: %w", err)
	}
	if metadata.Issuer != cfg.Issuer || !validEndpoint(metadata.JWKSURL, cfg.AllowInsecureLoopback, false) {
		return nil, errors.New("identity metadata must match the configured issuer and advertise an HTTPS key endpoint")
	}
	v.jwksURL = metadata.JWKSURL
	keys, err := v.fetchKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("load identity signing keys: %w", err)
	}
	v.keys, v.expires, v.nextRefresh = keys, v.now().Add(keyCacheTTL), v.now().Add(refreshInterval)
	return v, nil
}

// Verify takes the raw bearer token, without the Authorization scheme prefix.
// Returning an issuer/subject pair does not confer any Conductor permissions.
func (v *Verifier) Verify(ctx context.Context, bearer string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if len(bearer) == 0 || len(bearer) > MaxTokenBytes || strings.ContainsAny(bearer, " \t\r\n") {
		return Identity{}, ErrInvalidToken
	}
	token, err := jwt.ParseSigned(bearer, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(token.Headers) != 1 {
		return Identity{}, ErrInvalidToken
	}
	header := token.Headers[0]
	typ, _ := header.ExtraHeaders[jose.HeaderType].(string)
	// Media types are case insensitive (the RFC's own example uses at+JWT).
	if (!strings.EqualFold(typ, "at+jwt") && !strings.EqualFold(typ, "application/at+jwt")) || !validIdentifier(header.KeyID, 256) || header.JSONWebKey != nil {
		return Identity{}, ErrInvalidToken
	}
	for _, name := range []jose.HeaderKey{"crit", "b64", "jku", "x5u"} {
		if _, exists := header.ExtraHeaders[name]; exists {
			return Identity{}, ErrInvalidToken
		}
	}
	key, err := v.signingKey(ctx, header.KeyID)
	if err != nil {
		return Identity{}, err
	}
	var claims struct {
		jwt.Claims
		ClientID string `json:"client_id"`
	}
	if err := token.Claims(key, &claims); err != nil {
		return Identity{}, ErrInvalidToken
	}
	now := v.now()
	if claims.Expiry == nil || claims.IssuedAt == nil || !validIdentifier(claims.Subject, 512) || !validIdentifier(claims.ClientID, 512) || !validIdentifier(claims.ID, 512) {
		return Identity{}, ErrInvalidToken
	}
	// The library checks signature, issuer/audience, nbf and iat. Require the
	// profile's mandatory claims and strict expiration boundary in addition.
	if !now.Add(-v.skew).Before(claims.Expiry.Time()) || !claims.IssuedAt.Time().Before(claims.Expiry.Time()) || claims.ValidateWithLeeway(jwt.Expected{Issuer: v.issuer, AnyAudience: jwt.Audience{v.audience}, Time: now}, v.skew) != nil {
		return Identity{}, ErrInvalidToken
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	return Identity{Issuer: claims.Issuer, Subject: claims.Subject}, nil
}

func (v *Verifier) signingKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v.mu.Lock()
		now := v.now()
		if key := v.keys[kid]; key != nil && now.Before(v.expires) {
			v.mu.Unlock()
			return key, nil
		}
		if waiting := v.refreshing; waiting != nil {
			v.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-waiting:
				continue
			}
		}
		if now.Before(v.nextRefresh) {
			v.mu.Unlock()
			return nil, ErrInvalidToken
		}
		// Unknown kid values share one bounded refresh, including failed fetches.
		// Never refresh for every token or extend stale keys during an outage.
		v.refreshing = make(chan struct{})
		v.nextRefresh = now.Add(refreshInterval)
		v.mu.Unlock()
		keys, err := v.fetchKeys(ctx)
		v.mu.Lock()
		if err == nil {
			v.keys, v.expires = keys, v.now().Add(keyCacheTTL)
		}
		close(v.refreshing)
		v.refreshing = nil
		v.mu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrInvalidToken
		}
	}
}

func (v *Verifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := v.getJSON(ctx, v.jwksURL, &document); err != nil {
		return nil, err
	}
	if len(document.Keys) == 0 || len(document.Keys) > maxKeys {
		return nil, errors.New("signing key count is outside supported bounds")
	}
	keys := make(map[string]*rsa.PublicKey)
	seen := make(map[string]bool)
	for _, raw := range document.Keys {
		var metadata struct {
			ID         string   `json:"kid"`
			Type       string   `json:"kty"`
			Algorithm  string   `json:"alg"`
			Use        string   `json:"use"`
			Operations []string `json:"key_ops"`
		}
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return nil, errors.New("invalid signing key metadata")
		}
		if metadata.ID != "" && seen[metadata.ID] {
			return nil, errors.New("ambiguous duplicate signing key identifier")
		}
		seen[metadata.ID] = true
		if metadata.Type != "RSA" || (metadata.Algorithm != "" && metadata.Algorithm != "RS256") || (metadata.Use != "" && metadata.Use != "sig") {
			continue
		}
		if !validIdentifier(metadata.ID, 256) || (metadata.Operations != nil && (len(metadata.Operations) != 1 || metadata.Operations[0] != "verify")) {
			return nil, errors.New("unsupported signing key metadata")
		}
		var jwk jose.JSONWebKey
		if err := json.Unmarshal(raw, &jwk); err != nil || !jwk.Valid() || !jwk.IsPublic() {
			return nil, errors.New("invalid public signing key")
		}
		key, ok := jwk.Key.(*rsa.PublicKey)
		if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 {
			return nil, errors.New("unsupported RSA signing key size")
		}
		keys[metadata.ID] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("issuer has no supported RS256 signing keys")
	}
	return keys, nil
}

func (v *Verifier) getJSON(ctx context.Context, endpoint string, dest any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	response, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("identity endpoint returned a non-success status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDocumentBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxDocumentBytes {
		return errors.New("identity document exceeds the size limit")
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return errors.New("identity endpoint returned invalid JSON")
	}
	return nil
}

func validEndpoint(raw string, allowHTTP, issuer bool) bool {
	if !validIdentifier(raw, 2048) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (issuer && (u.RawQuery != "" || u.ForceQuery)) {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return allowHTTP && u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func validIdentifier(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
