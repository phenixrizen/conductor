package runtimeworker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
)

type activityStore struct {
	receipt         string
	checks, commits int
	deny            bool
}

func (s *activityStore) RuntimeOperationReceipt(context.Context, string, string) (string, error) {
	return s.receipt, nil
}
func (s *activityStore) CheckRuntimeWork(context.Context, string, string) (store.RuntimeWork, error) {
	s.checks++
	if s.deny {
		return store.RuntimeWork{}, domain.ErrForbidden
	}
	return store.RuntimeWork{CredentialID: "runtime", Evidence: domain.RuntimeEvidence{ID: strings.Repeat("a", 32)}}, nil
}
func (s *activityStore) CompleteRuntimeEvidence(context.Context, string, string, domain.RuntimeReceipt) (string, error) {
	s.commits++
	if s.deny {
		return "", domain.ErrForbidden
	}
	s.receipt = strings.Repeat("c", 64)
	return s.receipt, nil
}

type checkedCollector struct {
	check  func(context.Context) error
	called *int
}

func (c checkedCollector) Collect(ctx context.Context, _ domain.RuntimeEvidence) (domain.RuntimeReceipt, error) {
	*c.called++
	return domain.RuntimeReceipt{}, c.check(ctx)
}
func TestRuntimeActivityChecksBeforeCredentialsAndProviderThenRecoversReceipt(t *testing.T) {
	db := &activityStore{}
	credentials, provider := 0, 0
	a, err := NewActivity(db, func(context.Context, domain.RuntimeTarget, string) (string, error) {
		credentials++
		return "fixture-secret", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.newCollector = func(token string, check func(context.Context) error) (Collector, error) {
		if token != "fixture-secret" {
			t.Fatal("lost credential binding")
		}
		return checkedCollector{check, &provider}, nil
	}
	ref := contextworkflow.Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)}
	out, err := a.Collect(context.Background(), ref)
	if err != nil || out.Digest == "" || db.checks != 2 || credentials != 1 || provider != 1 || db.commits != 1 {
		t.Fatal("activity authority or receipt failed")
	}
	db.deny = true
	if _, err = a.Collect(context.Background(), ref); err != nil || db.checks != 2 || credentials != 1 || provider != 1 {
		t.Fatal("receipt retry repeated provider work")
	}
	db.receipt = ""
	if _, err = a.Collect(context.Background(), ref); !errors.Is(err, domain.ErrForbidden) || credentials != 1 {
		t.Fatal("credential read before authority")
	}
}
func TestRuntimeCredentialFileBinding(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("s", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Credentials{entries: []Credential{{ID: "runtime", WorkspaceID: "w", RepositoryID: "r", BackendID: "b", TokenFile: token}}}
	target := domain.RuntimeTarget{WorkspaceID: "w", RepositoryID: "r", BackendID: "b", Profile: domain.GroundcoverProfile}
	if value, err := c.Resolve(context.Background(), target, "runtime"); err != nil || len(value) != 32 {
		t.Fatal("token read failed")
	}
	target.RepositoryID = "other"
	if _, err := c.Resolve(context.Background(), target, "runtime"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("credential crossed repository")
	}
	if _, err := ReadOperatorFile(root, 8192); err == nil {
		t.Fatal("directory accepted")
	}
}
