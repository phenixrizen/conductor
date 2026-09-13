package authn

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
)

// ErrInvalidLogin never includes codes, client secrets, tokens, or issuer bodies.
var ErrInvalidLogin = errors.New("OpenID Connect login could not be verified")

type BrowserConfig struct {
	Issuer, ClientID, ClientSecret, RedirectURL string
	HTTPClient                                  *http.Client
	// Only synthetic local providers/callbacks may use this test escape hatch.
	AllowInsecureLoopback bool
}

// Browser is a confidential authorization-code client. The caller owns state,
// browser binding, one-time challenge consumption, and Conductor session cookies.
// Provider tokens stay inside Exchange; the result is only an issuer/subject.
type Browser struct {
	issuer      string
	keys        *issuerKeys
	oauth       oauth2.Config
	tokenClient *http.Client
	idTokens    *oidc.IDTokenVerifier
}

func NewBrowser(ctx context.Context, cfg BrowserConfig) (*Browser, error) {
	if !validIdentifier(cfg.ClientID, 512) || cfg.ClientSecret == "" || len(cfg.ClientSecret) > MaxTokenBytes || !utf8.ValidString(cfg.ClientSecret) || strings.IndexFunc(cfg.ClientSecret, unicode.IsControl) >= 0 {
		return nil, errors.New("browser login requires a client ID and bounded confidential client secret")
	}
	if !validEndpoint(cfg.RedirectURL, cfg.AllowInsecureLoopback, true) {
		return nil, errors.New("browser login requires a configured HTTPS callback URL without credentials, query, or fragment")
	}
	keys, err := discoverKeys(ctx, cfg.Issuer, cfg.HTTPClient, cfg.AllowInsecureLoopback)
	if err != nil {
		return nil, loginError(ctx)
	}
	m := keys.metadata
	if !validEndpoint(m.AuthorizationURL, cfg.AllowInsecureLoopback, false) || !validEndpoint(m.TokenURL, cfg.AllowInsecureLoopback, false) || !containsString(m.ResponseTypes, "code") || !containsString(m.IDTokenAlgorithms, "RS256") {
		return nil, errors.New("issuer must advertise HTTPS authorization/token endpoints, code response type, and RS256 ID tokens")
	}
	authorizationURL, _ := url.Parse(m.AuthorizationURL)
	for _, name := range []string{"client_id", "redirect_uri", "response_type", "response_mode", "scope", "state", "nonce", "code_challenge", "code_challenge_method"} {
		if authorizationURL.Query().Has(name) {
			return nil, errors.New("issuer authorization endpoint must not preselect authentication parameters")
		}
	}
	// OIDC discovery defaults an omitted auth-method list to client_secret_basic.
	// Explicit AuthStyle avoids the OAuth library's fallback exchange/retry.
	if m.TokenAuthMethods != nil && !containsString(m.TokenAuthMethods, "client_secret_basic") {
		return nil, errors.New("issuer must support client_secret_basic for browser login")
	}
	if m.PKCEMethods != nil && !containsString(m.PKCEMethods, "S256") {
		return nil, errors.New("issuer must support S256 PKCE for browser login")
	}
	b := &Browser{issuer: cfg.Issuer, keys: keys, oauth: oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: cfg.RedirectURL,
		Scopes:   []string{oidc.ScopeOpenID},
		Endpoint: oauth2.Endpoint{AuthURL: m.AuthorizationURL, TokenURL: m.TokenURL, AuthStyle: oauth2.AuthStyleInHeader},
	}}
	b.tokenClient = boundedHTTPClient(keys.client)
	base := b.tokenClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	tokenURL, _ := url.Parse(m.TokenURL)
	b.tokenClient.Transport = &tokenTransport{base: base, endpoint: tokenURL.String()}
	// The library supplies OIDC signature/claim validation. Our KeySet retains
	// hard cache expiry, throttled refresh, key bounds, and request cancellation.
	b.idTokens = oidc.NewVerifier(cfg.Issuer, browserKeys{keys}, &oidc.Config{
		ClientID: cfg.ClientID, SupportedSigningAlgs: []string{oidc.RS256},
		Now: func() time.Time { return keys.now() },
	})
	return b, nil
}

func GenerateCodeVerifier() string { return oauth2.GenerateVerifier() }

func (b *Browser) AuthorizationURL(state, nonce, verifier string) (string, error) {
	if !protocolSecret(state, 32, 256) || !protocolSecret(nonce, 32, 256) || !protocolSecret(verifier, 43, 128) {
		return "", ErrInvalidLogin
	}
	return b.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce), oauth2.SetAuthURLParam("response_mode", "query")), nil
}

// Exchange is called only after the caller atomically consumes the matching
// state/browser challenge. Ambiguous exchange failure requires a new login; it
// must never replay a one-use authorization code or create an optimistic session.
func (b *Browser) Exchange(ctx context.Context, code, verifier, expectedNonce string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	if !validIdentifier(code, 8192) || !protocolSecret(verifier, 43, 128) || !protocolSecret(expectedNonce, 32, 256) {
		return Identity{}, ErrInvalidLogin
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	requestCtx := context.WithValue(ctx, oauth2.HTTPClient, b.tokenClient)
	tokens, err := b.oauth.Exchange(requestCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, loginError(ctx)
	}
	rawID, ok := tokens.Extra("id_token").(string)
	if !ok || !strings.EqualFold(tokens.TokenType, "Bearer") || len(rawID) == 0 || len(rawID) > MaxTokenBytes || strings.ContainsAny(rawID, " \r\n\t") {
		return Identity{}, ErrInvalidLogin
	}
	parsed, err := jose.ParseSignedCompact(rawID, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(parsed.Signatures) != 1 || !validKeyHeader(parsed.Signatures[0].Header) {
		return Identity{}, ErrInvalidLogin
	}
	if value, present := parsed.Signatures[0].Header.ExtraHeaders[jose.HeaderType]; present {
		typ, ok := value.(string)
		if !ok || (!strings.EqualFold(typ, "JWT") && !strings.EqualFold(typ, "application/jwt")) {
			return Identity{}, ErrInvalidLogin
		}
	}
	id, err := b.idTokens.Verify(ctx, rawID)
	if err != nil {
		return Identity{}, loginError(ctx)
	}
	var claims struct {
		jwt.Claims
		AuthorizedParty json.RawMessage `json:"azp"`
	}
	if id.Claims(&claims) != nil || claims.Expiry == nil || claims.IssuedAt == nil || !validIdentifier(id.Subject, 512) || len(claims.Audience) != 1 || claims.Audience[0] != b.oauth.ClientID {
		return Identity{}, ErrInvalidLogin
	}
	now := b.keys.now()
	// Do not inherit provider exceptions or the library's five-minute nbf leeway.
	// A browser client ID is the only trusted ID-token audience in this slice.
	if claims.ValidateWithLeeway(jwt.Expected{Issuer: b.issuer, AnyAudience: jwt.Audience{b.oauth.ClientID}, Time: now}, 0) != nil || !now.Before(claims.Expiry.Time()) || !claims.IssuedAt.Time().Before(claims.Expiry.Time()) || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(expectedNonce)) != 1 {
		return Identity{}, ErrInvalidLogin
	}
	if len(claims.AuthorizedParty) != 0 {
		var party string
		if json.Unmarshal(claims.AuthorizedParty, &party) != nil || party != b.oauth.ClientID {
			return Identity{}, ErrInvalidLogin
		}
	}
	if id.AccessTokenHash != "" && id.VerifyAccessToken(tokens.AccessToken) != nil {
		return Identity{}, ErrInvalidLogin
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	return Identity{Issuer: id.Issuer, Subject: id.Subject}, nil
}

type browserKeys struct{ keys *issuerKeys }

func (k browserKeys) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	parsed, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(parsed.Signatures) != 1 || !validKeyHeader(parsed.Signatures[0].Header) {
		return nil, ErrInvalidLogin
	}
	key, err := k.keys.signingKey(ctx, parsed.Signatures[0].Header.KeyID)
	if err != nil {
		return nil, err
	}
	return parsed.Verify(key)
}

// Token responses contain credentials. Enforce limits before library parsing,
// and discard upstream errors instead of exposing their possibly echoed secrets.
type tokenTransport struct {
	base     http.RoundTripper
	endpoint string
}

func (t *tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.String() != t.endpoint {
		return nil, ErrInvalidLogin
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, ErrInvalidLogin
	}
	defer response.Body.Close()
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || mediaType != "application/json" {
		return nil, ErrInvalidLogin
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDocumentBytes+1))
	if err != nil || len(body) > maxDocumentBytes {
		return nil, ErrInvalidLogin
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

func protocolSecret(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func loginError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrInvalidLogin
}
