package reach

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Verifier checks a public URL by asking it for /api/health and matching the
// instance in the answer: "ok" when this server answered, "unverified"
// otherwise. Unverified is common from inside the network (a router that
// does not hairpin), so it never says unreachable.
type Verifier struct {
	Instance string
	Client   *http.Client
}

// Verify is Options.Verify.
func (v Verifier) Verify(ctx context.Context, publicURL string) string {
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicURL+"/api/health", nil)
	if err != nil {
		return "unverified"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "unverified"
	}
	defer resp.Body.Close()
	var body struct {
		Instance string `json:"instance"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil || body.Instance != v.Instance {
		return "unverified"
	}
	return "ok"
}
