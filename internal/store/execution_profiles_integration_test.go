package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestExecutionProfileCatalogUsesActualAdapterContractAndScopedAudit(t *testing.T) {
	ctx, p, s := collectionStore(t)
	s = s.WithCoordination()
	profile := domain.ExecutionProfileConfig{WorkspaceID: "workspace-one", ID: "synthetic", Image: "sha256:" + strings.Repeat("a", 64), Enabled: true, Profile: domain.WorkerProfile{Adapter: "command/v1", Command: []string{"/bin/sh", "-c", "printf synthetic"}}}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{profile}}); err != nil {
		t.Fatal(err)
	}
	selected := accessContext(ctx, "worker", "workspace-one", "repo-one")
	page, err := s.ExecutionProfiles(selected)
	if err != nil || page.Truncated || len(page.Profiles) != 1 {
		t.Fatalf("catalog: %+v %v", page, err)
	}
	_, digest, err := ValidatedExecutionProfile(profile.Profile)
	if err != nil || page.Profiles[0].ProfileDigest != digest || page.Profiles[0].Image != profile.Image {
		t.Fatalf("profile binding: %+v %v", page, err)
	}
	other, err := s.ExecutionProfiles(accessContext(ctx, "reviewer", "workspace-two", "repo-other-workspace"))
	if err != nil || len(other.Profiles) != 0 {
		t.Fatalf("workspace catalog leaked: %+v %v", other, err)
	}
	if _, err = s.ExecutionProfiles(accessContext(ctx, "worker", "workspace-one", "repo-two")); err == nil {
		t.Fatal("catalog exposed through unreadable repository")
	}
	invalid := profile
	invalid.Profile.Adapter = "invented-assistant/v1"
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{invalid}}); err == nil {
		t.Fatal("invented adapter was provisioned")
	}
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_profile_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$; CREATE TRIGGER reject_profile_audit BEFORE INSERT ON access_audit_events FOR EACH ROW EXECUTE FUNCTION reject_profile_audit()`); err != nil {
		t.Fatal(err)
	}
	profile.Enabled = false
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{profile}}); err == nil {
		t.Fatal("unaudited profile revocation committed")
	}
	page, err = s.ExecutionProfiles(selected)
	if err != nil || len(page.Profiles) != 1 {
		t.Fatalf("audit did not roll back: %+v %v", page, err)
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER reject_profile_audit ON access_audit_events`); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{profile}}); err != nil {
		t.Fatal(err)
	}
	page, err = s.ExecutionProfiles(selected)
	if err != nil || len(page.Profiles) != 0 {
		t.Fatalf("disabled profile visible: %+v %v", page, err)
	}
}

func TestExecutionProfileCatalogTruncationIsExplicit(t *testing.T) {
	ctx, p, s := collectionStore(t)
	s = s.WithCoordination()
	var config domain.AccessConfig
	for i := 0; i < 101; i++ {
		config.ExecutionProfiles = append(config.ExecutionProfiles, domain.ExecutionProfileConfig{WorkspaceID: "workspace-one", ID: fmt.Sprintf("profile-%03d", i), Image: "sha256:" + strings.Repeat("a", 64), Enabled: true, Profile: domain.WorkerProfile{Adapter: "command/v1", Command: []string{"/bin/true"}}})
	}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	page, err := s.ExecutionProfiles(accessContext(ctx, "reader", "workspace-one", "repo-one"))
	if err != nil || len(page.Profiles) != 100 || !page.Truncated || page.Profiles[0].ID != "profile-000" || page.Profiles[99].ID != "profile-099" {
		t.Fatalf("catalog bound: %+v %v", page, err)
	}
}
