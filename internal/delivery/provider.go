package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

var (
	ErrProvider = errors.New("provider rejected publication operation")
	ErrUnknown  = errors.New("provider outcome is unknown; reconcile before retry")
	ErrMissing  = errors.New("provider record is unavailable")
	ErrMismatch = errors.New("provider identity or exact artifact does not match")
)

const MaxProviderRequests = 320
const MaxProviderResponseBytes = 2 << 20
const MaxProviderTotalBytes = 64 << 20

type Provider struct {
	target domain.DeliveryTarget
	token  string
	http   *http.Client
	base   string
}

func NewProvider(target domain.DeliveryTarget, token string) (*Provider, error) {
	if token == "" || len(token) > 16<<10 || strings.ContainsAny(token, "\r\n\x00") {
		return nil, ErrProvider
	}
	provider := &Provider{target: target, token: token}
	if target.Provider == "github" && target.Host == "github.com" && target.Profile == domain.GitHubDeliveryProfile {
		if domain.ValidateDeliveryIntegration(domain.DeliveryIntegrationConfig{WorkspaceID: target.WorkspaceID, RepositoryID: target.RepositoryID, Profile: target.Profile, Locator: target.Locator, CredentialID: "binding", BaseBranches: []string{"main"}}) != nil {
			return nil, ErrMismatch
		}
		provider.base = "https://api.github.com/repos/" + target.Locator
	} else if target.Provider == "gitlab" && target.Host == "gitlab.com" && target.Profile == domain.GitLabDeliveryProfile && target.Locator == "" {
		provider.base = "https://gitlab.com/api/v4/projects/" + url.PathEscape(target.ProviderID)
	} else {
		return nil, ErrProvider
	}
	id, err := strconv.ParseInt(target.ProviderID, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != target.ProviderID {
		return nil, ErrMismatch
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	provider.http = &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return provider, nil
}

// callBudget exists only for one bounded activity invocation. Access is rechecked
// before every external operation. Transport retries, redirects, proxies and
// cookie credentials are disabled so uncertainty cannot disappear in HTTP code.
type callBudget struct {
	provider        *Provider
	check           func(context.Context) error
	requests, bytes int
}

func (b *callBudget) call(ctx context.Context, method, path string, input, output any) (http.Header, error) {
	if b.requests >= MaxProviderRequests || b.bytes >= MaxProviderTotalBytes {
		return nil, ErrProvider
	}
	if err := b.check(ctx); err != nil {
		return nil, err
	}
	b.requests++
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil || len(body) > 48<<20 {
			return nil, ErrProvider
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, b.provider.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrProvider
	}
	req.GetBody = nil
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	if b.provider.target.Provider == "github" {
		req.Header.Set("Authorization", "Bearer "+b.provider.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	} else {
		req.Header.Set("PRIVATE-TOKEN", b.provider.token)
	}
	response, err := b.provider.http.Do(req)
	if err != nil {
		return nil, ErrUnknown
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxProviderResponseBytes+1))
	b.bytes += len(raw)
	if err != nil || len(raw) > MaxProviderResponseBytes || b.bytes > MaxProviderTotalBytes {
		return nil, ErrUnknown
	}
	if response.StatusCode == 404 {
		return response.Header, ErrMissing
	}
	if response.StatusCode >= 300 {
		if response.StatusCode == 429 || response.StatusCode >= 500 {
			return response.Header, ErrUnknown
		}
		return response.Header, ErrProvider
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return response.Header, ErrUnknown
	}
	return response.Header, nil
}
func (p *Provider) checkRepository(ctx context.Context, b *callBudget) error {
	var identity struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	}
	if _, err := b.call(ctx, "GET", "", nil, &identity); err != nil {
		return err
	}
	if strconv.FormatInt(identity.ID, 10) != p.target.ProviderID || p.target.Provider == "github" && !strings.EqualFold(identity.FullName, p.target.Locator) {
		return ErrMismatch
	}
	return nil
}
func marker(d domain.Delivery) string {
	return "<!-- conductor-publication:" + d.ID + ":" + d.Digest + " -->"
}
func commitMessage(d domain.Delivery) string {
	return d.Input.Title + "\n\nConductor-Publication: " + d.ID + "\nConductor-Artifact: " + d.Input.ArtifactDigest + "\n"
}
func body(d domain.Delivery) string {
	return d.Input.Description + "\n\n" + marker(d) + "\n\nConductor retained artifact: `" + d.Input.ArtifactDigest + "`. Baseline: `" + d.BaseCommit + "`. Result tree: `" + d.ResultTree + "`. Provider checks and merge remain separate observed facts.\n"
}
func observation(d domain.Delivery) domain.DeliveryObservation {
	return domain.DeliveryObservation{Commit: "", Tree: d.ResultTree, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "not_observed", ProductionOutcome: "not_observed", ObservedAt: time.Now().UTC()}
}
func validURL(raw, host string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == host && u.User == nil && u.Fragment == "" && len(raw) <= 2048
}
func (p *Provider) Publish(ctx context.Context, d domain.Delivery, artifact Prepared, check func(context.Context) error) (domain.DeliveryObservation, error) {
	if check == nil || d.Target != p.target || !domain.IsLowerHex(d.ID, 32) || !domain.IsLowerHex(d.Digest, 64) || d.Branch != "conductor/publication/"+d.ID || artifact.BaseCommit != d.BaseCommit || artifact.BaseTree != d.BaseTree || artifact.ResultTree != d.ResultTree || artifact.PatchDigest != d.PatchDigest {
		return domain.DeliveryObservation{}, ErrMismatch
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	budget := &callBudget{provider: p, check: check}
	if err := p.checkRepository(ctx, budget); err != nil {
		return domain.DeliveryObservation{}, err
	}
	var result domain.DeliveryObservation
	var err error
	if p.target.Provider == "github" {
		result, err = p.publishGitHub(ctx, budget, d, artifact)
	} else {
		result, err = p.publishGitLab(ctx, budget, d, artifact)
	}
	if err != nil {
		return result, err
	}
	if err = p.checkRepository(ctx, budget); err != nil {
		return domain.DeliveryObservation{}, err
	}
	return result, nil
}
func (p *Provider) Observe(ctx context.Context, d domain.Delivery, check func(context.Context) error) (domain.DeliveryObservation, error) {
	if check == nil || d.Target != p.target || d.Observation == nil {
		return domain.DeliveryObservation{}, ErrMismatch
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	budget := &callBudget{provider: p, check: check}
	if err := p.checkRepository(ctx, budget); err != nil {
		return domain.DeliveryObservation{}, err
	}
	if p.target.Provider == "github" {
		return p.observeGitHub(ctx, budget, d, d.Observation.Number, d.Observation.Commit)
	}
	return p.observeGitLab(ctx, budget, d, d.Observation.Number, d.Observation.Commit)
}
