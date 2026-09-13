package acceptance_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/trackerworker"
	"github.com/phenixrizen/conductor/internal/trackerworkflow"
)

// Real browser, signed identity, PostgreSQL and tracker HTTP adapters. A test-owned
// activity pump supplies scheduling; owned Temporal recovery has its own suite.
func TestBrowserWorkspaceTrackers(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for tracker browser acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	for _, providerName := range []string{"linear", "jira"} {
		t.Run(providerName, func(t *testing.T) {
			f := newBrowserFixture(t, true)
			config := trackerConfig(providerName)
			for i := range config.Grants {
				if config.Grants[i].PrincipalID == "person-reviewer" {
					config.Grants[i].CanResolve = true
				}
			}
			if err := f.db.ApplyTrackerConfig(f.ctx, "synthetic-operator", config); err != nil {
				t.Fatal(err)
			}
			provider, factory := newTrackerProvider(t, config)
			activity, err := trackerworker.NewActivity(f.db, trackerCredentials, factory)
			if err != nil {
				t.Fatal(err)
			}
			agent := trackerClient(t, f.accessFixture, "agent")
			shared := createTrackerLink(t, f.accessFixture, agent, provider, false)
			runTrackerActivity(t, f.accessFixture, activity, shared.LatestSyncID)
			pkg := f.create("author", "team", "application", domain.Content{"intent": "Browser tracker link cannot grant approval"})
			input := domain.TrackerLinkInput{IssueID: provider.issue, Packages: []domain.TrackerPackageRef{{RepositoryID: "application", PackageID: pkg.ID, Revision: 1, Digest: pkg.Revision.Digest}}}
			encoded, _ := json.Marshal(input)
			file := filepath.Join(t.TempDir(), "link.json")
			if err = os.WriteFile(file, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			workerCtx, cancel := context.WithCancel(f.ctx)
			done := make(chan struct{})
			t.Cleanup(func() { cancel(); <-done })
			go func() {
				defer close(done)
				timer := time.NewTicker(25 * time.Millisecond)
				defer timer.Stop()
				edited := false
				for {
					select {
					case <-workerCtx.Done():
						return
					case <-timer.C:
					}
					rows, err := f.sql.Query(workerCtx, `SELECT s.id,s.binding,s.input->>'mode' FROM tracker_syncs s WHERE NOT EXISTS(SELECT 1 FROM tracker_observations o WHERE o.sync_id=s.id) ORDER BY s.created_at LIMIT 4`)
					if err != nil {
						if workerCtx.Err() == nil {
							t.Error(err)
						}
						return
					}
					type item struct{ id, binding, mode string }
					var items []item
					for rows.Next() {
						var v item
						if err = rows.Scan(&v.id, &v.binding, &v.mode); err != nil {
							t.Error(err)
							rows.Close()
							return
						}
						items = append(items, v)
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						if workerCtx.Err() == nil {
							t.Error(err)
						}
						return
					}
					for _, v := range items {
						if _, err = activity.Sync(workerCtx, trackerworkflow.Reference{ID: v.id, Binding: v.binding}); err != nil {
							if workerCtx.Err() == nil {
								t.Error(err)
							}
							return
						}
						if v.mode == "publish" && !edited {
							// Model a later external edit after the completed first publication. The
							// next explicit refresh must expose a conflict requiring human resolution.
							provider.mu.Lock()
							if provider.projection != nil {
								provider.projection.Title = "External edit <script>window.trackerInjected=true</script>"
								edited = true
							}
							provider.mu.Unlock()
						}
					}
				}
			}()
			python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
			if python == "" {
				python = "python3"
			}
			command := exec.CommandContext(f.ctx, python, "../browser/tracking.py")
			command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_TRACKER_PROVIDER="+providerName, "CONDUCTOR_BROWSER_TRACKER_LINK_ID="+shared.ID, "CONDUCTOR_BROWSER_TRACKER_LINK_DIGEST="+shared.Digest, "CONDUCTOR_BROWSER_TRACKER_FILE="+file)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("tracker browser: %v\n%s", err, output)
			}
			t.Log(string(output))
			var count int
			if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM tracker_links WHERE creator_id='person-reviewer'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("link retry count: %d %v", count, err)
			}
			var approved bool
			if err = f.sql.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM approvals WHERE change_id=$1)`, pkg.ID).Scan(&approved); err != nil {
				t.Fatal(err)
			}
			if approved {
				t.Fatal("ticket state granted approval")
			}
		})
	}
}
