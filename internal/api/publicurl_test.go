package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/config"
)

// A share link is built on the address its request came through while
// publicUrl names localhost (the default), with a reverse proxy's forwarded
// scheme and host when they are there; a publicUrl that names another
// machine is used as it is.
func TestShareLinksTakeTheRequestsAddressWhenPublicURLIsLocal(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	id := e.createSession("cat")
	create := func(headers map[string]string) string {
		req, _ := http.NewRequest("POST", e.http.URL+"/api/sessions/"+id+"/links", strings.NewReader(`{"role":"view"}`))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, out)
		}
		return out["url"].(string)
	}
	host := strings.TrimPrefix(e.http.URL, "http://")
	if u := create(nil); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("plain: %s", u)
	}
	if u := create(map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "conductor.example.com"}); !strings.HasPrefix(u, "https://conductor.example.com/join/") {
		t.Fatalf("forwarded: %s", u)
	}
	if u := create(map[string]string{"X-Forwarded-Host": "evil/../x"}); !strings.HasPrefix(u, "http://localhost:8080/join/") {
		t.Fatalf("a malformed forwarded host fell back to publicUrl: %s", u)
	}
	fixed := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://team.example.net" })
	fid := fixed.createSession("cat")
	_, out := fixed.do("POST", "/api/sessions/"+fid+"/links", adminToken, map[string]any{"role": "view"})
	if u, _ := out["url"].(string); !strings.HasPrefix(u, "https://team.example.net/join/") {
		t.Fatalf("fixed: %v", out["url"])
	}
}
