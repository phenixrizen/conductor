package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/pkg/client"
)

func TestRemoteContextCommandsRequireAuthenticatedCanonicalScope(t *testing.T) {
	for _, command := range []string{"context-collect", "context-collections", "context-collection", "context-cancel", "context-attach"} {
		for _, mode := range []string{"local", "workspace missing", "repository missing", "mixed actor", "valid"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				cleanCredentialEnvironment(t)
				actor, workspace, repository := "", "team", "application"
				if mode != "local" {
					t.Setenv("CONDUCTOR_TOKEN", "synthetic.token")
				} else {
					actor, workspace, repository = "operator", "", ""
				}
				if mode == "workspace missing" {
					workspace = ""
				}
				if mode == "repository missing" {
					repository = ""
				}
				if mode == "mixed actor" {
					actor = "operator"
				}
				_, err := clientForCommand(command, actor, actor != "", workspace, repository)
				if (err == nil) != (mode == "valid") {
					t.Fatalf("unexpected credential result: %v", err)
				}
			})
		}
	}
}

func TestRemoteContextCLIValidatesCommandShapeBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		options collectionOptions
	}{
		{command: "context-collect"},
		{command: "context-collect", options: collectionOptions{commit: strings.Repeat("a", 40), paths: []string{"README.md"}}},
		{command: "context-collect", args: []string{"extra"}, options: collectionOptions{commit: strings.Repeat("a", 40), paths: []string{"README.md"}, key: "request-1"}},
		{command: "context-collections", options: collectionOptions{limit: 101}},
		{command: "context-collections", args: []string{"extra"}, options: collectionOptions{limit: 20}},
		{command: "context-collection"},
		{command: "context-cancel", args: []string{"COL-first", "COL-second"}},
		{command: "context-attach", args: []string{"CHG-inspected"}},
		{command: "context-attach", args: []string{"CHG-inspected"}, options: collectionOptions{collectionID: "COL-inspected", digest: strings.Repeat("b", 64)}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			// A nil client makes any accidental network path fail the test.
			if _, err := runCollectionCommand(context.Background(), nil, tc.command, tc.args, tc.options); err == nil {
				t.Fatal("incomplete command accepted")
			}
		})
	}
}

func TestRemoteContextCLIPreservesInspectedCommandsWithoutPreflight(t *testing.T) {
	for _, tc := range []struct {
		command, method, path, key string
		args                       []string
		options                    collectionOptions
		body                       any
	}{
		{command: "context-collect", method: "POST", path: "/api/v1/context-collections", key: "explicit-request-1",
			options: collectionOptions{commit: strings.Repeat("a", 40), paths: []string{"README.md", "file,with,comma.md"}, key: "explicit-request-1"},
			body:    map[string]any{"commit": strings.Repeat("a", 40), "paths": []any{"README.md", "file,with,comma.md"}}},
		{command: "context-collections", method: "GET", path: "/api/v1/context-collections?before=opaque-cursor&limit=7", options: collectionOptions{before: "opaque-cursor", limit: 7}},
		{command: "context-collection", method: "GET", path: "/api/v1/context-collections/COL-inspected", args: []string{"COL-inspected"}},
		{command: "context-cancel", method: "POST", path: "/api/v1/context-collections/COL-inspected/cancellation", args: []string{"COL-inspected"}, body: map[string]any{}},
		{command: "context-attach", method: "POST", path: "/api/v1/changes/CHG-inspected/context-attachments", args: []string{"CHG-inspected"},
			options: collectionOptions{collectionID: "COL-inspected", digest: strings.Repeat("b", 64), expectedRevision: 7},
			body:    map[string]any{"expectedRevision": float64(7), "collectionId": "COL-inspected", "digest": strings.Repeat("b", 64)}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.RequestURI != tc.path || r.Header.Get("Idempotency-Key") != tc.key {
					t.Errorf("unexpected request %s %s", r.Method, r.RequestURI)
				}
				if tc.body != nil {
					var body any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body, tc.body) {
						t.Errorf("request body=%#v want=%#v", body, tc.body)
					}
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			c, err := client.NewAuthenticated(server.URL, "synthetic.token", "team", "application")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runCollectionCommand(context.Background(), c, tc.command, tc.args, tc.options); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("one user command made %d requests", calls)
			}
		})
	}
}

func TestRemoteContextCLIParsesFlagsIntoSingleAuthenticatedCommand(t *testing.T) {
	for _, command := range []string{"context-collect", "context-attach"} {
		t.Run(command, func(t *testing.T) {
			cleanCredentialEnvironment(t)
			commit, collectionID, digest := strings.Repeat("a", 40), strings.Repeat("c", 32), strings.Repeat("b", 64)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic.token" ||
					r.Header.Get("X-Conductor-Workspace") != "team" || r.Header.Get("X-Conductor-Repository") != "application" ||
					len(r.Header.Values("X-Conductor-Actor")) != 0 {
					t.Error("CLI changed command identity or scope")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if command == "context-collect" {
					if r.URL.Path != "/api/v1/context-collections" || r.Header.Get("Idempotency-Key") != "explicit-request-1" ||
						body["commit"] != commit || !reflect.DeepEqual(body["paths"], []any{"README.md", "file,with,comma.md"}) {
						t.Errorf("collect flags changed inspected input: path=%s body=%+v", r.URL.Path, body)
					}
				} else if r.URL.Path != "/api/v1/changes/CHG-inspected/context-attachments" ||
					body["expectedRevision"] != float64(7) || body["collectionId"] != collectionID || body["digest"] != digest {
					t.Errorf("attachment flags changed inspected tuple: path=%s body=%+v", r.URL.Path, body)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			t.Setenv("CONDUCTOR_TOKEN", "synthetic.token")
			t.Setenv("CONDUCTOR_URL", server.URL)
			original := os.Args
			t.Cleanup(func() { os.Args = original })
			os.Args = []string{"conductor", command, "--workspace", "team", "--repository-id", "application"}
			if command == "context-collect" {
				os.Args = append(os.Args, "--commit", commit, "--path", "README.md", "--path", "file,with,comma.md", "--idempotency-key", "explicit-request-1")
			} else {
				os.Args = append(os.Args, "--expected-revision", "7", "--collection-id", collectionID, "--digest", digest, "CHG-inspected")
			}
			main()
			if calls != 1 {
				t.Fatalf("CLI made %d requests for one confirmed command", calls)
			}
		})
	}
}
