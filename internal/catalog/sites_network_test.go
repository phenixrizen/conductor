//go:build network

package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/publicsuffix"
)

// TestSitesAnswer fetches every built-in agent's site (by-hand item 7 of
// round 4): a HEAD then a GET, 15 s each, redirects allowed only within the
// site's registrable domain, a final 2xx. Runs with -tags network (make
// test-network, nightly).
func TestSitesAnswer(t *testing.T) {
	for _, a := range Default().List() {
		if a.Site == "" {
			continue
		}
		t.Run(a.ID, func(t *testing.T) {
			t.Parallel()
			if err := fetchSite(a.Site); err != nil {
				t.Errorf("%s: %s: %v", a.ID, a.Site, err)
			}
		})
	}
}

func fetchSite(site string) error {
	base, err := url.Parse(site)
	if err != nil {
		return err
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(base.Hostname())
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if r, err := publicsuffix.EffectiveTLDPlusOne(req.URL.Hostname()); err != nil || r != root {
				return fmt.Errorf("redirected off the site to %s", req.URL)
			}
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var last error
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, _ := http.NewRequestWithContext(ctx, method, site, nil)
		req.Header.Set("User-Agent", "conductor-sites-check/1 (+https://github.com/phenixrizen/conductor)")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
		if method == http.MethodHead && (resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound) {
			continue // some sites refuse HEAD; GET decides
		}
	}
	if last == nil {
		last = errors.New("no answer")
	}
	return fmt.Errorf("%w (final host must stay under %s)", last, strings.TrimSpace(root))
}
