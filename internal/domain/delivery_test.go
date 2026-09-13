package domain

import (
	"testing"
	"time"
)

func TestDeliveryDigestBindsProposalAndExcludesLaterFacts(t *testing.T) {
	d := Delivery{ID: "proposal", Input: DeliveryInput{Title: "Inspected"}, BaseCommit: "base", ResultTree: "result", Branch: "branch"}
	digest, err := DeliveryProposalDigest(d)
	if err != nil {
		t.Fatal(err)
	}
	d.CreatedAt = time.Now().UTC()
	d.Authorization = &DeliveryAuthorization{Actor: "human"}
	d.Observation = &DeliveryObservation{State: "merged"}
	d.Receipt = &DeliveryReceipt{Digest: "receipt"}
	same, err := DeliveryProposalDigest(d)
	if err != nil || same != digest {
		t.Fatal("later evidence changed inspected proposal")
	}
	d.Input.Title = "Changed"
	changed, err := DeliveryProposalDigest(d)
	if err != nil || changed == digest {
		t.Fatal("edit retained authorization digest")
	}
}

func TestDeliveryLocatorPermitsRepositoryDotsWithoutPathEscapes(t *testing.T) {
	config := DeliveryIntegrationConfig{WorkspaceID: "workspace", RepositoryID: "repo", CredentialID: "credential", Profile: GitHubDeliveryProfile, Locator: "synthetic/repo.name", BaseBranches: []string{"main"}}
	if ValidateDeliveryIntegration(config) != nil {
		t.Fatal("ordinary GitHub repository rejected")
	}
	for _, locator := range []string{"synthetic/..", "synthetic/repo%2fname", "https://github.com/synthetic/repo", "synthetic/repo?query", "synthetic/repo name"} {
		config.Locator = locator
		if ValidateDeliveryIntegration(config) == nil {
			t.Fatalf("unsafe locator %q", locator)
		}
	}
}
