package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestTrackerPublicationReferencesKeepReceiptsAndCurrentProviderEvidence(t *testing.T) {
	ctx, p, s, input := deliveryFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	delivery, e := s.CreateDelivery(human, "tracker-publication", input)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthorizeDelivery(human, delivery.ID, delivery.Digest); e != nil {
		t.Fatal(e)
	}
	item, e := p.ClaimDeliveryDispatch(ctx, strings.Repeat("d", 64), "default")
	if e != nil || item == nil {
		t.Fatal(e)
	}
	observed := domain.DeliveryObservation{ProviderID: "99", Number: 1, URL: "https://github.com/synthetic/repo/pull/1", Commit: strings.Repeat("e", 40), Tree: delivery.ResultTree, State: "draft", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "not_observed", ProductionOutcome: "not_observed", ObservedAt: time.Now().UTC()}
	if _, e = p.CompleteDeliveryOperation(ctx, item.ID, item.Work.Binding, observed); e != nil {
		t.Fatal(e)
	}
	published, e := s.GetDelivery(human, delivery.ID)
	if e != nil || published.Receipt == nil {
		t.Fatal("missing immutable publication receipt", e)
	}
	config := domain.TrackerConfig{WorkspaceID: "workspace-one", Provider: "linear", Host: "linear.app", OrganizationID: "11111111-1111-4111-8111-111111111111", ScopeID: "22222222-2222-4222-8222-222222222222", Profile: domain.LinearTrackerProfile, CredentialID: "tracker-api", WebhookCredentialID: "tracker-hook", ConductorOrigin: "https://conductor.example.invalid", Enabled: true, Statuses: []domain.TrackerStatusMapping{{ID: "44444444-4444-4444-8444-444444444444", Display: "Planned"}}, Grants: []domain.TrackerGrant{{PrincipalID: "human-reviewer", CanRead: true, CanSync: true, CanResolve: true}}}
	if e = p.ApplyTrackerConfig(ctx, "synthetic-operator", config); e != nil {
		t.Fatal(e)
	}
	run, e := s.GetCoordination(human, input.RunID)
	if e != nil {
		t.Fatal(e)
	}
	in := domain.TrackerLinkInput{IssueID: "33333333-3333-4333-8333-333333333333", Publications: []domain.TrackerPublicationRef{{ID: published.ID, Digest: published.Receipt.Digest}}}
	for _, pin := range run.Plan.Packages {
		in.Packages = append(in.Packages, domain.TrackerPackageRef{PackageID: pin.ChangeID, RepositoryID: pin.RepositoryID, Revision: pin.Revision, Digest: pin.Digest})
	}
	link, e := s.CreateTrackerLink(human, "provider-link", in)
	if e != nil {
		t.Fatal(e)
	}
	if len(link.Publications) != 1 || link.Publications[0].Target.Provider != "github" || link.Publications[0].Target.ProviderID != published.Target.ProviderID || link.Publications[0].Receipt.Digest != published.Receipt.Digest || !strings.Contains(link.Projection.Summary, observed.URL) {
		t.Fatalf("canonical publication provenance lost: %+v", link)
	}
	in.Publications[0].Digest = strings.Repeat("f", 64)
	if _, e = s.CreateTrackerLink(human, "forged-provider-link", in); !errors.Is(e, domain.ErrNotFound) {
		t.Fatalf("forged publication receipt accepted: %v", e)
	}
	// An independent newer provider observation does not rewrite the pinned receipt.
	// This structural SQL fixture does not claim an actual provider merge occurred.
	observed.State = "merged"
	observed.Draft = false
	observed.MergeCommit = strings.Repeat("f", 40)
	observed.ObservedAt = time.Now().UTC()
	if _, e = p.pool.Exec(ctx, `INSERT INTO delivery_observations(delivery_id,observation) VALUES($1,$2)`, published.ID, observed); e != nil {
		t.Fatal(e)
	}
	link, e = s.GetTrackerLink(human, link.ID)
	if e != nil || link.Publications[0].Observation.State != "merged" || link.Publications[0].Receipt.Observation.State != "draft" {
		t.Fatalf("current evidence replaced historical receipt: %+v %v", link.Publications, e)
	}
	if e = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanRead: false}}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetTrackerLink(human, link.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatalf("hidden publication source leaked: %v", e)
	}
	page, e := s.ListTrackerLinks(human, "", 1)
	if e != nil || len(page.Links) != 0 || page.Truncated || page.Next != "" {
		t.Fatalf("hidden publication pagination leaked: %+v %v", page, e)
	}
}
