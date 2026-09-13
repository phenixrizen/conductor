// Package authn verifies identity only. Workspace membership and review authority
// come from Conductor's database, never token roles, groups, or email addresses.
package authn

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	*issuerKeys
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
	keys, err := discoverKeys(ctx, cfg.Issuer, cfg.HTTPClient, cfg.AllowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	return &Verifier{issuer: cfg.Issuer, audience: cfg.Audience, skew: cfg.ClockSkew, issuerKeys: keys}, nil
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
	if (!strings.EqualFold(typ, "at+jwt") && !strings.EqualFold(typ, "application/at+jwt")) || !validKeyHeader(header) {
		return Identity{}, ErrInvalidToken
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

func validEndpoint(raw string, allowHTTP, issuer bool) bool {
	if !validIdentifier(raw, 2048) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || strings.Contains(raw, "#") || (issuer && (u.RawQuery != "" || u.ForceQuery)) {
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
