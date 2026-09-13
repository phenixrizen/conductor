package store

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

const collectionCommit = "1111111111111111111111111111111111111111"

func collectionAccessConfig() domain.AccessConfig {
	cfg := syntheticAccessConfig()
	for i := range cfg.Repositories {
		cfg.Repositories[i].Host = cfg.Repositories[i].Provider + ".com"
	}
	cfg.Principals = append(cfg.Principals, domain.PrincipalConfig{ID: "human-reader", Issuer: "https://identity.example.test", Subject: "reader", Kind: "human", Active: true})
	cfg.Memberships = append(cfg.Memberships, domain.MembershipConfig{WorkspaceID: "workspace-one", PrincipalID: "human-reader", Active: true})
	cfg.Grants = append(cfg.Grants, domain.GrantConfig{RepositoryID: "repo-one", PrincipalID: "human-reader", CanRead: true})
	for _, repo := range cfg.Repositories {
		in := domain.ContextIntegrationConfig{RepositoryID: repo.ID, WorkspaceID: repo.WorkspaceID, CredentialID: "synthetic-provider-credential", Enabled: true}
		if repo.Provider == "github" {
			in.Profile, in.Locator = "github-rest/2026-03-10", "synthetic/context"
		} else {
			in.Profile = "gitlab-rest/v4-19.3"
		}
		cfg.ContextIntegrations = append(cfg.ContextIntegrations, in)
	}
	return cfg
}
func collectionStore(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService) {
	t.Helper()
	ctx, p, _ := integrationStore(t)
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", collectionAccessConfig()); err != nil {
		t.Fatal(err)
	}
	return ctx, p, service.NewAuthenticated(p).WithCollections()
}
func collectionRequest(t *testing.T, ctx context.Context, s *service.AuthenticatedService, key string) domain.Collection {
	t.Helper()
	got, err := s.CreateCollection(ctx, key, domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func collectionWork(t *testing.T, ctx context.Context, p *Postgres, id string) CollectionWork {
	t.Helper()
	var binding string
	if err := p.pool.QueryRow(ctx, `SELECT binding FROM context_collections WHERE id=$1`, id).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	work, err := p.CollectionWork(ctx, id, binding)
	if err != nil {
		t.Fatal(err)
	}
	return work
}
func collectionArtifacts(text string) []domain.ContextArtifact {
	git := sha1.Sum(append([]byte(fmt.Sprintf("blob %d%c", len(text), 0)), []byte(text)...))
	digest := sha256.Sum256([]byte(text))
	return []domain.ContextArtifact{{Path: "source.txt", State: "collected", BlobOID: hex.EncodeToString(git[:]), Digest: hex.EncodeToString(digest[:]), Text: &text}}
}
func collectionReceipt(t *testing.T, ctx context.Context, p *Postgres, c domain.Collection) domain.CollectionReceipt {
	t.Helper()
	work := collectionWork(t, ctx, p, c.ID)
	r, err := p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("synthetic shared source\n"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func collectionCounts(t *testing.T, ctx context.Context, p *Postgres) [4]int {
	t.Helper()
	var counts [4]int
	if err := p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM context_collections),(SELECT count(*) FROM context_receipts),(SELECT count(*) FROM context_outbox),(SELECT count(*) FROM context_audit_events)`).Scan(&counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
		t.Fatal(err)
	}
	return counts
}
func collectionConfigUpdate(t *testing.T, ctx context.Context, p *Postgres, change func(*domain.ContextIntegrationConfig)) {
	t.Helper()
	in := collectionAccessConfig().ContextIntegrations[0]
	change(&in)
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ContextIntegrations: []domain.ContextIntegrationConfig{in}}); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionRequestIdentityPermissionsAndDiscovery(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	reader := accessContext(ctx, "reader", "workspace-one", "repo-one")
	input := domain.CollectionInput{Commit: collectionCommit, Paths: []string{"z.txt", "a.txt"}}
	c, err := s.CreateCollection(author, "stable-request", input)
	if err != nil {
		t.Fatal(err)
	}
	if c.RequesterID != "human-author" || c.RepositoryID != "repo-one" || c.WorkspaceID != "workspace-one" || !reflect.DeepEqual(c.Input.Paths, []string{"a.txt", "z.txt"}) {
		t.Fatalf("canonical request: %+v", c)
	}
	input.Paths = []string{"a.txt", "z.txt"}
	again, err := s.CreateCollection(author, "stable-request", input)
	if err != nil || !reflect.DeepEqual(c, again) {
		t.Fatalf("reordered retry changed request: %+v %v", again, err)
	}
	input.Commit = strings.Repeat("2", 40)
	if _, err = s.CreateCollection(author, "stable-request", input); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	if _, err = s.CreateCollection(reader, "reader-request", input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reader used provider credential: %v", err)
	}
	if _, err = service.NewAuthenticated(p).CreateCollection(author, "disabled-server", input); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("server capability gate: %v", err)
	}
	agent := collectionRequest(t, accessContext(ctx, "worker", "workspace-one", "repo-one"), s, "stable-request")
	if agent.RequesterID != "agent-worker" || agent.ID == c.ID {
		t.Fatal("agent author attribution or requester idempotency scope lost")
	}
	private := collectionRequest(t, accessContext(ctx, "reviewer", "workspace-one", "repo-two"), s, "private")
	other := collectionRequest(t, accessContext(ctx, "reviewer", "workspace-two", "repo-other-workspace"), s, "other-workspace")
	for _, id := range []string{private.ID, other.ID} {
		if _, err = s.GetCollection(reader, id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("cross-scope collection visible: %v", err)
		}
	}
	var all []string
	cursor := ""
	for {
		page, err := s.ListCollections(reader, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Collections) > 1 {
			t.Fatal("unbounded collection page")
		}
		for _, item := range page.Collections {
			if item.RepositoryID != "repo-one" {
				t.Fatal("permission filtering happened after pagination")
			}
			all = append(all, item.ID)
		}
		if page.NextBefore == "" {
			break
		}
		if page.NextBefore == cursor {
			t.Fatal("cursor did not progress")
		}
		cursor = page.NextBefore
	}
	if len(all) != 2 || all[0] != agent.ID || all[1] != c.ID {
		t.Fatalf("shared keyset listing = %v", all)
	}
	work := collectionWork(t, ctx, p, c.ID)
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), work.Binding) || strings.Contains(string(b), work.BindingDigest) || strings.Contains(string(b), "synthetic-provider-credential") {
		t.Fatal("trusted worker binding or credential reference leaked through public collection")
	}
	if _, err = p.CollectionWork(ctx, c.ID, "bad"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("malformed worker nonce: %v", err)
	}
	if _, err = p.CollectionWork(ctx, c.ID, strings.Repeat("0", 32)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forged worker nonce: %v", err)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{4, 0, 4, 4} {
		t.Fatalf("unexpected committed facts: %v", got)
	}
}

func TestCollectionConcurrentIdempotencyAndAdmission(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	type result struct {
		collection domain.Collection
		err        error
	}
	results := make(chan result, 30)
	start := make(chan struct{})
	for range 12 {
		go func() {
			<-start
			c, err := s.CreateCollection(author, "same-concurrent-key", domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}})
			results <- result{c, err}
		}()
	}
	close(start)
	id := ""
	for range 12 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if id != "" && id != r.collection.ID {
			t.Fatal("duplicate retry created another request")
		}
		id = r.collection.ID
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{1, 0, 1, 1} {
		t.Fatalf("concurrent retry facts: %v", got)
	}
	start = make(chan struct{})
	for i := range 24 {
		go func(i int) {
			<-start
			c, err := s.CreateCollection(author, fmt.Sprintf("capacity-%02d", i), domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}})
			results <- result{c, err}
		}(i)
	}
	close(start)
	success, capacity := 0, 0
	for range 24 {
		r := <-results
		switch {
		case r.err == nil:
			success++
		case errors.Is(r.err, domain.ErrCapacity):
			capacity++
		default:
			t.Fatalf("concurrent admission: %v", r.err)
		}
	}
	if success != 19 || capacity != 5 {
		t.Fatalf("capacity successes=%d denials=%d", success, capacity)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{20, 0, 20, 20} {
		t.Fatalf("admission facts: %v", got)
	}
	// A retry consumes no new slot. Cancellation intent alone cannot release a
	// slot because Temporal has not yet confirmed that the execution stopped.
	if _, err := s.CreateCollection(author, "same-concurrent-key", domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelCollection(author, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCollection(author, "capacity-after-cancel", domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatalf("unconfirmed cancellation released active capacity: %v", err)
	}
	var otherID string
	if err := p.pool.QueryRow(ctx, `SELECT id FROM context_collections WHERE id<>$1 ORDER BY id LIMIT 1`, id).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	work := collectionWork(t, ctx, p, otherID)
	if _, err := p.CompleteCollection(ctx, otherID, work.Binding, collectionArtifacts("completed context")); err != nil {
		t.Fatal(err)
	}
	collectionRequest(t, author, s, "capacity-after-receipt")
	collectionRequest(t, accessContext(ctx, "reviewer", "workspace-one", "repo-two"), s, "independent-repository")
}

func TestCollectionIntegrationBindingAndRevocation(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	collectionConfigUpdate(t, ctx, p, func(in *domain.ContextIntegrationConfig) { in.Enabled = false })
	if _, err := s.CreateCollection(author, "disabled", domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("disabled integration admitted request: %v", err)
	}
	collectionConfigUpdate(t, ctx, p, func(in *domain.ContextIntegrationConfig) { in.Enabled = true })
	c := collectionRequest(t, author, s, "bound")
	work := collectionWork(t, ctx, p, c.ID)
	if err := p.CheckCollectionWork(ctx, c.ID, work.Binding); err != nil {
		t.Fatal(err)
	}
	collectionConfigUpdate(t, ctx, p, func(in *domain.ContextIntegrationConfig) { in.Enabled = false })
	if err := p.CheckCollectionWork(ctx, c.ID, work.Binding); !errors.Is(err, domain.ErrCollectionStopped) {
		t.Fatalf("disabled integration kept provider authority: %v", err)
	}
	// Updating the same enabled binding increments provenance but preserves the
	// request's exact original source version and credential reference.
	collectionConfigUpdate(t, ctx, p, func(in *domain.ContextIntegrationConfig) { in.Enabled = true })
	if err := p.CheckCollectionWork(ctx, c.ID, work.Binding); err != nil {
		t.Fatal(err)
	}
	collectionConfigUpdate(t, ctx, p, func(in *domain.ContextIntegrationConfig) { in.Locator = "synthetic/renamed" })
	if err := p.CheckCollectionWork(ctx, c.ID, work.Binding); !errors.Is(err, domain.ErrCollectionStopped) {
		t.Fatalf("changed locator kept old authority: %v", err)
	}
	again := collectionRequest(t, author, s, "bound")
	if again.Source != c.Source {
		t.Fatal("retry silently rebound integration")
	}
	newRequest := collectionRequest(t, author, s, "new-binding")
	if newRequest.Source.Locator != "synthetic/renamed" || newRequest.Source.IntegrationVersion <= c.Source.IntegrationVersion {
		t.Fatal("new explicit request did not bind current source")
	}
	if _, err := p.pool.Exec(ctx, `UPDATE context_collections SET binding_digest=$2 WHERE id=$1`, c.ID, strings.Repeat("0", 64)); err == nil {
		t.Fatal("immutable request binding changed")
	}
	if _, err := p.pool.Exec(ctx, `UPDATE managed_repositories SET provider_id='999' WHERE id='repo-one'`); err == nil {
		t.Fatal("canonical repository identity changed")
	}
	collectionConfigUpdate(t, ctx, p, func(in *domain.ContextIntegrationConfig) { in.CredentialID = "replacement-reference" })
	if err := p.CheckCollectionWork(ctx, c.ID, work.Binding); !errors.Is(err, domain.ErrCollectionStopped) {
		t.Fatalf("credential reference repointed old request: %v", err)
	}
}

func TestCollectionFinalPermissionCheckAndTrustedReceiptRecovery(t *testing.T) {
	for _, kind := range []string{"author", "reader", "membership", "principal", "integration"} {
		t.Run(kind, func(t *testing.T) {
			ctx, p, s := collectionStore(t)
			author := accessContext(ctx, "author", "workspace-one", "repo-one")
			completed := collectionRequest(t, author, s, "completed")
			receipt := collectionReceipt(t, ctx, p, completed)
			done := collectionWork(t, ctx, p, completed.ID)
			pending := collectionRequest(t, author, s, "pending")
			work := collectionWork(t, ctx, p, pending.ID)
			if err := p.CheckCollectionWork(ctx, pending.ID, work.Binding); err != nil {
				t.Fatal(err)
			}
			cfg := domain.AccessConfig{}
			switch kind {
			case "author":
				cfg.Grants = []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true}}
			case "reader":
				cfg.Grants = []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author"}}
			case "membership":
				cfg.Memberships = []domain.MembershipConfig{{WorkspaceID: "workspace-one", PrincipalID: "human-author", Active: false}}
			case "principal":
				cfg.Principals = []domain.PrincipalConfig{{ID: "human-author", Issuer: "https://identity.example.test", Subject: "author", Kind: "human", Active: false}}
			case "integration":
				in := collectionAccessConfig().ContextIntegrations[0]
				in.Enabled = false
				cfg.ContextIntegrations = []domain.ContextIntegrationConfig{in}
			}
			if err := p.ApplyAccessConfig(ctx, "synthetic-revocation", cfg); err != nil {
				t.Fatal(err)
			}
			if err := p.CheckCollectionWork(ctx, pending.ID, work.Binding); err == nil {
				t.Fatal("revocation did not fence later provider operations")
			}
			if _, err := p.CompleteCollection(ctx, pending.ID, work.Binding, collectionArtifacts("discard after revocation")); err == nil {
				t.Fatal("revoked result was published")
			}
			recovered, err := p.CompleteCollection(ctx, completed.ID, done.Binding, collectionArtifacts("must not replace first receipt"))
			if err != nil || !reflect.DeepEqual(recovered, receipt) {
				t.Fatalf("trusted retry lost committed receipt: %+v %v", recovered, err)
			}
			if kind == "reader" || kind == "membership" || kind == "principal" {
				if _, err = s.GetCollection(author, completed.ID); err == nil {
					t.Fatal("historical source exposed to revoked requester")
				}
			}
			shared, err := s.GetCollection(accessContext(ctx, "reader", "workspace-one", "repo-one"), completed.ID)
			if err != nil || shared.Receipt == nil || !reflect.DeepEqual(*shared.Receipt, receipt) {
				t.Fatalf("authorized collaborator lost source: %+v %v", shared, err)
			}
		})
	}
}

func TestCollectionReceiptValidationAndFirstWriter(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "receipt")
	work := collectionWork(t, ctx, p, c.ID)
	bad := collectionArtifacts("invalid path")
	bad[0].Path = "other.txt"
	if _, err := p.CompleteCollection(ctx, c.ID, work.Binding, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("wrong result paths: %v", err)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{1, 0, 1, 1} {
		t.Fatalf("invalid result persisted facts: %v", got)
	}
	type result struct {
		receipt domain.CollectionReceipt
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, text := range []string{"first observed source", "different concurrent observation"} {
		go func(text string) {
			<-start
			r, e := p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts(text))
			results <- result{r, e}
		}(text)
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || !reflect.DeepEqual(a.receipt, b.receipt) {
		t.Fatalf("concurrent receipts did not converge: %+v %+v", a, b)
	}
	r := a.receipt
	if r.Snapshot.SchemaVersion != 2 || r.Snapshot.Collector != domain.RemoteContextCollector || r.Snapshot.CollectionID != c.ID || r.Snapshot.Source == nil || *r.Snapshot.Source != c.Source || !r.CreatedAt.Equal(r.Snapshot.CollectedAt) {
		t.Fatalf("receipt provenance: %+v", r)
	}
	if err := domain.ValidateCollectionReceipt(r, c); err != nil {
		t.Fatal(err)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{1, 1, 1, 2} {
		t.Fatalf("duplicate completion facts: %v", got)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE context_receipts SET digest=$2 WHERE collection_id=$1`, c.ID, strings.Repeat("0", 64)); err == nil {
		t.Fatal("receipt rewritten")
	}
	if _, err := p.pool.Exec(ctx, `DELETE FROM context_receipts WHERE collection_id=$1`, c.ID); err == nil {
		t.Fatal("receipt deleted")
	}
}

func TestCollectionCancellationOwnershipAndResultOrder(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "cancel")
	work := collectionWork(t, ctx, p, c.ID)
	if _, err := s.CancelCollection(accessContext(ctx, "reviewer", "workspace-one", "repo-one"), c.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("other author cancelled request: %v", err)
	}
	if _, err := s.CancelCollection(accessContext(ctx, "reader", "workspace-one", "repo-one"), c.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reader cancelled request: %v", err)
	}
	cancelled, err := s.CancelCollection(author, c.ID)
	if err != nil || cancelled.CancelRequestedAt == nil || cancelled.Execution != nil {
		t.Fatalf("cancel intent falsely confirmed execution: %+v %v", cancelled, err)
	}
	again, err := s.CancelCollection(author, c.ID)
	if err != nil || !reflect.DeepEqual(again, cancelled) {
		t.Fatalf("duplicate cancellation changed fact: %+v %v", again, err)
	}
	if err = p.CheckCollectionWork(ctx, c.ID, work.Binding); !errors.Is(err, domain.ErrCollectionStopped) {
		t.Fatalf("cancelled provider operation: %v", err)
	}
	if _, err = p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("late source")); !errors.Is(err, domain.ErrCollectionStopped) {
		t.Fatalf("cancelled result publication: %v", err)
	}
	done := collectionRequest(t, author, s, "result-first")
	receipt := collectionReceipt(t, ctx, p, done)
	retained, err := s.CancelCollection(author, done.ID)
	if err != nil || retained.CancelRequestedAt != nil || retained.Receipt == nil || !reflect.DeepEqual(*retained.Receipt, receipt) {
		t.Fatalf("cancellation erased completion: %+v %v", retained, err)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{2, 1, 3, 4} {
		t.Fatalf("cancellation facts: %v", got)
	}
}

func TestCollectionCancellationAndCompletionSerialize(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	for i := range 8 {
		c := collectionRequest(t, author, s, fmt.Sprintf("race-%d", i))
		work := collectionWork(t, ctx, p, c.ID)
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() { <-start; _, err := s.CancelCollection(author, c.ID); results <- err }()
		go func() {
			<-start
			_, err := p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("racing source"))
			results <- err
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil && !errors.Is(err, domain.ErrCollectionStopped) {
				t.Fatalf("cancellation/result race: %v", err)
			}
		}
		observed, err := s.GetCollection(author, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (observed.Receipt == nil) == (observed.CancelRequestedAt == nil) {
			t.Fatalf("race has contradictory/no committed outcome: %+v", observed)
		}
	}
}

func TestCollectionAttachmentPreservesHistoryAndScope(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "attach")
	receipt := collectionReceipt(t, ctx, p, c)
	content := domain.Content{"intent": "synthetic package", "futureExtension": map[string]any{"preserve": true}}
	pkg, err := s.Create(author, "ignored", content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(author, pkg.ID, "ignored", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Approve(reviewer, pkg.ID, "ignored", 1, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachCollection(author, pkg.ID, 2, c.ID, receipt.Digest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale attachment revision: %v", err)
	}
	if _, err = s.AttachCollection(author, pkg.ID, 1, c.ID, strings.Repeat("0", 64)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("incorrect inspected receipt digest: %v", err)
	}
	attached, err := s.AttachCollection(author, pkg.ID, 1, c.ID, receipt.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Revision.Number != 2 || attached.Approved || attached.Approval != nil || attached.Revision.SubmittedAt != nil || !reflect.DeepEqual(attached.Revision.Content["futureExtension"], content["futureExtension"]) {
		t.Fatalf("attachment carried approval or lost extension: %+v", attached)
	}
	historical, err := s.Revision(reviewer, pkg.ID, 1)
	if err != nil || len(historical.Approvals) != 1 || historical.Revision.Digest != pkg.Revision.Digest || !reflect.DeepEqual(historical.Revision.Content, content) {
		t.Fatalf("attachment changed history: %+v %v", historical, err)
	}
	if _, err = s.AttachCollection(author, pkg.ID, 2, c.ID, receipt.Digest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("identical attachment created redundant revision: %v", err)
	}
	for _, scope := range [][2]string{{"workspace-one", "repo-two"}, {"workspace-two", "repo-other-workspace"}} {
		otherCtx := accessContext(ctx, "reviewer", scope[0], scope[1])
		otherPkg, err := s.Create(otherCtx, "ignored", domain.Content{"intent": "isolated package"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.AttachCollection(otherCtx, otherPkg.ID, 1, c.ID, receipt.Digest); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("cross-canonical-scope attachment accepted: %v", err)
		}
	}
	// Every public collection command requires canonical repository selection,
	// including when the actor can read and author both repositories.
	other := collectionRequest(t, accessContext(ctx, "reviewer", "workspace-one", "repo-two"), s, "other-repo")
	otherReceipt := collectionReceipt(t, ctx, p, other)
	if _, err = s.AttachCollection(accessContext(ctx, "reviewer", "workspace-one", ""), pkg.ID, 2, other.ID, otherReceipt.Digest); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unselected scope crossed repository ownership: %v", err)
	}
	second := collectionRequest(t, author, s, "second-attachment")
	secondReceipt := collectionReceipt(t, ctx, p, second)
	if _, err = s.AttachCollection(author, pkg.ID, 2, second.ID, secondReceipt.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachCollection(author, pkg.ID, 3, c.ID, receipt.Digest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("attachment bypassed existing historical-digest/no-revert invariant: %v", err)
	}
}

func TestCollectionOrdinaryCommandsVerifyReceiptProvenance(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "provenance")
	receipt := collectionReceipt(t, ctx, p, c)
	var snapshot map[string]any
	b, _ := json.Marshal(receipt.Snapshot)
	if err := json.Unmarshal(b, &snapshot); err != nil {
		t.Fatal(err)
	}
	content := domain.Content{"intent": "genuine imported receipt", "repositoryContext": snapshot, "futurePackageExtension": map[string]any{"preserve": true}}
	pkg, err := s.Create(author, "ignored", content)
	if err != nil {
		t.Fatalf("genuine snapshot with outer package extensions rejected: %v", err)
	}
	if !reflect.DeepEqual(pkg.Revision.Content, content) {
		t.Fatal("receipt round trip discarded unknown fields")
	}
	content["intent"] = "genuine revision"
	pkg, err = s.Revise(author, pkg.ID, "ignored", 1, content)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown_id", "changed_text", "other_workspace", "other_repository", "snapshot_extension", "source_extension", "artifact_extension"} {
		t.Run(kind, func(t *testing.T) {
			var forged map[string]any
			if err := json.Unmarshal(b, &forged); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unknown_id":
				forged["collectionId"] = strings.Repeat("0", 32)
			case "changed_text":
				forged["artifacts"] = collectionArtifacts("forged but internally consistent text")
			case "other_workspace":
				forged["source"].(map[string]any)["workspaceId"] = "workspace-two"
			case "other_repository":
				forged["source"].(map[string]any)["repositoryId"] = "repo-two"
			case "snapshot_extension":
				forged["futureExtension"] = "not covered by the trusted receipt"
			case "source_extension":
				forged["source"].(map[string]any)["futureExtension"] = "not covered by the trusted receipt"
			case "artifact_extension":
				forged["artifacts"].([]any)[0].(map[string]any)["futureExtension"] = "not covered by the trusted receipt"
			}
			bad := domain.Content{"intent": "forged provenance", "repositoryContext": forged}
			if _, err := s.Create(author, "ignored", bad); err == nil {
				t.Fatal("ordinary create accepted forged provenance")
			}
			if _, err := s.Revise(author, pkg.ID, "ignored", 2, bad); err == nil {
				t.Fatal("ordinary revise accepted forged provenance")
			}
		})
	}
	if _, err = service.New(p).Create(ctx, "local-actor", content); err == nil {
		t.Fatal("local mode imported authenticated provenance")
	}
	if _, err = s.Create(accessContext(ctx, "reviewer", "workspace-two", "repo-other-workspace"), "ignored", content); err == nil {
		t.Fatal("same external repository in another workspace imported receipt")
	}
	current, err := s.Get(author, pkg.ID)
	if err != nil || current.Revision.Number != 2 {
		t.Fatalf("forged revise changed current revision: %+v %v", current, err)
	}
}

func TestCollectionAuditFailureRollsBackAtomicFacts(t *testing.T) {
	for _, command := range []string{"request", "receipt", "cancel", "attach"} {
		t.Run(command, func(t *testing.T) {
			ctx, p, s := collectionStore(t)
			author := accessContext(ctx, "author", "workspace-one", "repo-one")
			var c domain.Collection
			var receipt domain.CollectionReceipt
			var pkg domain.Package
			if command != "request" {
				c = collectionRequest(t, author, s, "audit")
			}
			if command == "attach" {
				receipt = collectionReceipt(t, ctx, p, c)
				var err error
				pkg, err = s.Create(author, "ignored", domain.Content{"intent": "audit rollback"})
				if err != nil {
					t.Fatal(err)
				}
			}
			counts := collectionCounts(t, ctx, p)
			packageCounts := integrationCounts(t, ctx, p)
			event := map[string]string{"request": "collection.requested", "receipt": "collection.receipt_recorded", "cancel": "collection.cancellation_requested", "attach": "collection.attached"}[command]
			if _, err := p.pool.Exec(ctx, `ALTER TABLE context_audit_events ADD CONSTRAINT fail_collection_audit CHECK (event_type <> '`+event+`')`); err != nil {
				t.Fatal(err)
			}
			var err error
			switch command {
			case "request":
				_, err = s.CreateCollection(author, "audit", domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}})
			case "receipt":
				work := collectionWork(t, ctx, p, c.ID)
				_, err = p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("rollback source"))
			case "cancel":
				_, err = s.CancelCollection(author, c.ID)
			case "attach":
				_, err = s.AttachCollection(author, pkg.ID, 1, c.ID, receipt.Digest)
			}
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "23514" || databaseError.ConstraintName != "fail_collection_audit" {
				t.Fatalf("missing injected audit failure: %v", err)
			}
			if got := collectionCounts(t, ctx, p); got != counts {
				t.Fatalf("failed %s partially committed collection facts: %v -> %v", command, counts, got)
			}
			if got := integrationCounts(t, ctx, p); got != packageCounts {
				t.Fatalf("failed %s partially committed package facts: %v -> %v", command, packageCounts, got)
			}
			if command == "cancel" {
				current, err := s.GetCollection(author, c.ID)
				if err != nil || current.CancelRequestedAt != nil {
					t.Fatal("failed audited cancellation retained intent")
				}
			}
			if command == "attach" {
				current, err := s.Get(author, pkg.ID)
				if err != nil || !reflect.DeepEqual(current, pkg) {
					t.Fatal("failed audited attachment changed package")
				}
			}
		})
	}
}

func TestCollectionVersionOneExtensionsRemainOrdinaryContent(t *testing.T) {
	ctx, p, _ := collectionStore(t)
	local := service.New(p)
	v1 := map[string]any{"schemaVersion": 1, "repository": "synthetic-local", "commit": collectionCommit, "requestedRef": collectionCommit, "collector": domain.RepositoryContextCollector, "collectedAt": time.Now().UTC(), "artifacts": collectionArtifacts("local source"), "source": "unrelated future source extension", "collectionId": map[string]any{"oldExtension": true}}
	content := domain.Content{"intent": "preserve version one extension semantics", "repositoryContext": v1}
	pkg, err := local.Create(ctx, "local-author", content)
	if err != nil {
		t.Fatalf("existing version-one extension rejected: %v", err)
	}
	expected, _ := domain.Digest(content)
	if pkg.Revision.Digest != expected {
		t.Fatal("version-one digest changed")
	}
	content["intent"] = "ordinary revision preserves extension"
	updated, err := local.Revise(ctx, pkg.ID, "local-author", 1, content)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(content)
	var normalized domain.Content
	if err = json.Unmarshal(b, &normalized); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Revision.Content, normalized) {
		t.Fatal("legacy source extension lost in persistence")
	}
}

func TestCollectionMissingIntegrationCannotAdmitWork(t *testing.T) {
	ctx, p, _ := integrationStore(t)
	config := collectionAccessConfig()
	config.ContextIntegrations = nil
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	s := service.NewAuthenticated(p).WithCollections()
	_, err := s.CreateCollection(accessContext(ctx, "author", "workspace-one", "repo-one"), "missing-integration", domain.CollectionInput{Commit: collectionCommit, Paths: []string{"source.txt"}})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("missing integration admitted work: %v", err)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{} {
		t.Fatalf("missing integration left request facts: %v", got)
	}
}

func TestCollectionReceiptHoldsAuthorityUntilAuditCommit(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "permission-transaction")
	work := collectionWork(t, ctx, p, c.ID)
	// Pause the final audit INSERT after all authority checks. A concurrent
	// operator update must wait until the same receipt transaction commits.
	digest := sha256.Sum256([]byte(p.pool.Config().ConnConfig.RuntimeParams["search_path"]))
	lockID := int64(binary.BigEndian.Uint32(digest[:4])&0x7fffffff) + 1
	blocker, err := p.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = blocker.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID) }()
	if _, err = p.pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION hold_collection_receipt_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='collection.receipt_recorded' THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END $$; CREATE TRIGGER hold_collection_receipt_audit BEFORE INSERT ON context_audit_events FOR EACH ROW EXECUTE FUNCTION hold_collection_receipt_audit()`, lockID)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("transaction-bound source"))
		done <- err
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for waiting := false; !waiting; {
		if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=0 AND objid=$1::oid)`, lockID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-tick.C:
		case err := <-done:
			t.Fatalf("receipt ended before audit barrier: %v", err)
		case <-deadline.C:
			t.Fatal("receipt never reached audit barrier")
		}
	}
	for _, config := range []domain.AccessConfig{
		{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true}}},
		{ContextIntegrations: []domain.ContextIntegrationConfig{{RepositoryID: "repo-one", WorkspaceID: "workspace-one", Profile: "github-rest/2026-03-10", Locator: "synthetic/context", CredentialID: "synthetic-provider-credential", Enabled: false}}},
	} {
		limited, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		err = p.ApplyAccessConfig(limited, "synthetic-operator", config)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("revocation crossed receipt/audit transaction: %v", err)
		}
	}
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true}}}); err != nil {
		t.Fatal(err)
	}
	if err = p.CheckCollectionWork(ctx, c.ID, work.Binding); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("later provider request retained author grant: %v", err)
	}
	if got := collectionCounts(t, ctx, p); got != [4]int{1, 1, 1, 2} {
		t.Fatalf("authority transaction changed receipt facts: %v", got)
	}
}

func TestCollectionExecutionObservationsAreBoundedAndExplicit(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	wants := make(map[string]bool)
	for _, row := range []struct {
		state   string
		age     int
		current bool
	}{{"running", 0, true}, {"completed", 60, false}, {"unavailable", 0, false}} {
		c := collectionRequest(t, author, s, "observation-"+row.state)
		if _, err := p.pool.Exec(ctx, `INSERT INTO context_runtime_bindings(collection_id,target,namespace) VALUES($1,$2,'synthetic-context')`, c.ID, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := p.pool.Exec(ctx, `INSERT INTO context_execution_observations(collection_id,namespace,workflow_id,run_id,state,observed_at) VALUES($1,'synthetic-context',$2,'00000000-0000-0000-0000-000000000001',$3,clock_timestamp()-($4::int*interval '1 second'))`, c.ID, "conductor.collect.v1/"+c.ID, row.state, row.age); err != nil {
			t.Fatal(err)
		}
		wants[c.ID] = row.current
	}
	page, err := s.ListCollections(accessContext(ctx, "reader", "workspace-one", "repo-one"), "", 20)
	if err != nil || len(page.Collections) != len(wants) {
		t.Fatalf("observation listing: %+v %v", page, err)
	}
	for _, item := range page.Collections {
		if item.Execution == nil || item.Execution.Current != wants[item.ID] {
			t.Fatalf("listing inferred or omitted progress: %+v", item)
		}
		inspected, err := s.GetCollection(author, item.ID)
		if err != nil || inspected.Execution == nil || !reflect.DeepEqual(inspected.Execution, item.Execution) {
			t.Fatalf("list and inspect observations differ: %+v %v", inspected, err)
		}
		for _, stamp := range []time.Time{item.CreatedAt, item.Execution.ObservedAt, inspected.CreatedAt} {
			if _, offset := stamp.Zone(); offset != 0 {
				t.Fatalf("public observation timestamp is not UTC: %s", stamp)
			}
		}
	}
}

func TestCollectionConcurrentReceiptRemainsAuthoritativeAfterQueuedRevocation(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "receipt-revocation-order")
	work := collectionWork(t, ctx, p, c.ID)
	blocker, err := p.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	digest := sha256.Sum256([]byte(c.ID))
	lockID := int64(binary.BigEndian.Uint32(digest[:4])&0x7fffffff) + 1
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = blocker.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID) }()
	if _, err = p.pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION queue_collection_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='collection.receipt_recorded' THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END $$; CREATE TRIGGER queue_collection_receipt BEFORE INSERT ON context_audit_events FOR EACH ROW EXECUTE FUNCTION queue_collection_receipt()`, lockID)); err != nil {
		t.Fatal(err)
	}
	waitFor := func(query string, args ...any) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			var waiting bool
			if err := blocker.QueryRow(ctx, query, args...).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("expected transaction ordering did not reach its lock barrier")
			}
		}
	}
	type result struct {
		receipt domain.CollectionReceipt
		err     error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		r, e := p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("first committed observation"))
		first <- result{r, e}
	}()
	waitFor(`SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=0 AND objid=$1::oid)`, lockID)
	revoked := make(chan error, 1)
	go func() {
		revoked <- p.ApplyAccessConfig(ctx, "synthetic-revocation", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "human-author", Issuer: "https://identity.example.test", Subject: "author", Kind: "human", Active: false}}})
	}()
	// The operator's UPDATE is queued behind the first receipt's authority lock.
	waitFor(`SELECT EXISTS(SELECT 1 FROM pg_locks owner WHERE owner.relation='access_principals'::regclass AND owner.mode='RowExclusiveLock' AND EXISTS(SELECT 1 FROM pg_locks blocked WHERE blocked.pid=owner.pid AND NOT blocked.granted))`)
	go func() {
		r, e := p.CompleteCollection(ctx, c.ID, work.Binding, collectionArtifacts("must never replace the first observation"))
		second <- result{r, e}
	}()
	// This second attempt has read the pre-commit request and is queued behind
	// revocation. Receipt recovery must still win when that gate later denies.
	waitFor(`SELECT EXISTS(SELECT 1 FROM pg_locks owner WHERE owner.relation='access_principals'::regclass AND owner.mode='RowShareLock' AND EXISTS(SELECT 1 FROM pg_locks blocked WHERE blocked.pid=owner.pid AND NOT blocked.granted AND blocked.locktype IN ('tuple','transactionid')))`)
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
	a, b := <-first, <-second
	if err = <-revoked; err != nil {
		t.Fatal(err)
	}
	if a.err != nil || b.err != nil || !reflect.DeepEqual(a.receipt, b.receipt) {
		t.Fatalf("receipt lost behind queued revocation: first=%v second=%v firstDigest=%s secondDigest=%s", a.err, b.err, a.receipt.Digest, b.receipt.Digest)
	}
}
