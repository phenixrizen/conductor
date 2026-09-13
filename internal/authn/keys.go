package authn

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Both token profiles share metadata, network bounds, and signing-key policy.
// Claim validation remains separate: an ID token never becomes an API token.
type issuerKeys struct {
	client      *http.Client
	jwksURL     string
	metadata    issuerMetadata
	now         func() time.Time
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	expires     time.Time
	nextRefresh time.Time
	refreshing  chan struct{}
}

type issuerMetadata struct {
	Issuer            string   `json:"issuer"`
	JWKSURL           string   `json:"jwks_uri"`
	AuthorizationURL  string   `json:"authorization_endpoint"`
	TokenURL          string   `json:"token_endpoint"`
	ResponseTypes     []string `json:"response_types_supported"`
	IDTokenAlgorithms []string `json:"id_token_signing_alg_values_supported"`
	TokenAuthMethods  []string `json:"token_endpoint_auth_methods_supported"`
	PKCEMethods       []string `json:"code_challenge_methods_supported"`
}

func discoverKeys(ctx context.Context, issuer string, configured *http.Client, allowHTTP bool) (*issuerKeys, error) {
	if !validEndpoint(issuer, allowHTTP, true) {
		return nil, errors.New("authentication requires an HTTPS issuer")
	}
	v := &issuerKeys{client: boundedHTTPClient(configured), now: time.Now}
	if err := v.getJSON(ctx, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", &v.metadata); err != nil {
		return nil, fmt.Errorf("discover identity issuer: %w", err)
	}
	if v.metadata.Issuer != issuer || !validEndpoint(v.metadata.JWKSURL, allowHTTP, false) {
		return nil, errors.New("identity metadata must match the configured issuer and advertise an HTTPS key endpoint")
	}
	v.jwksURL = v.metadata.JWKSURL
	keys, err := v.fetchKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("load identity signing keys: %w", err)
	}
	v.keys, v.expires, v.nextRefresh = keys, v.now().Add(keyCacheTTL), v.now().Add(refreshInterval)
	return v, nil
}

func boundedHTTPClient(configured *http.Client) *http.Client {
	client := http.Client{Timeout: requestTimeout}
	if configured != nil {
		client = *configured
		if client.Timeout <= 0 || client.Timeout > requestTimeout {
			client.Timeout = requestTimeout
		}
	}
	// Identity endpoints require no browser cookies or HTTP redirect handling.
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func validKeyHeader(header jose.Header) bool {
	if !validIdentifier(header.KeyID, 256) || header.JSONWebKey != nil {
		return false
	}
	for _, name := range []jose.HeaderKey{"crit", "b64", "jku", "x5u"} {
		if _, exists := header.ExtraHeaders[name]; exists {
			return false
		}
	}
	return true
}

func (v *issuerKeys) signingKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
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

func (v *issuerKeys) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
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

func (v *issuerKeys) getJSON(ctx context.Context, endpoint string, dest any) error {
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
