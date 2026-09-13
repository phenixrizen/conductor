package deliveryworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/delivery"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/store"
)

func activityGit(t *testing.T) ([]byte, execution.Patch) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		raw, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git: %v %s", err, raw)
		}
		return raw
	}
	run("init", "-q")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.invalid")
	path := filepath.Join(dir, "source.txt")
	if os.WriteFile(path, []byte("base\n"), 0600) != nil {
		t.Fatal("fixture source")
	}
	run("add", ".")
	run("commit", "-qm", "base")
	patch := execution.Patch{RepositoryID: "repo", BaseCommit: strings.TrimSpace(string(run("rev-parse", "HEAD"))), BaseTree: strings.TrimSpace(string(run("rev-parse", "HEAD^{tree}"))), Paths: []string{"source.txt"}}
	bundlePath := filepath.Join(t.TempDir(), "source.bundle")
	run("bundle", "create", bundlePath, "HEAD")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if os.WriteFile(path, []byte("verified\n"), 0600) != nil {
		t.Fatal("fixture source")
	}
	run("add", ".")
	patch.ResultTree = strings.TrimSpace(string(run("write-tree")))
	patch.Patch = run("diff", "--cached", "--binary", "--full-index")
	patch.Digest = execution.Sum(patch.Patch)
	return bundle, patch
}

type activityDB struct {
	work            store.DeliveryWork
	receipt         string
	commits, checks int
}

func (d *activityDB) LoadDeliveryOperation(context.Context, string, string) (store.DeliveryOperation, error) {
	return store.DeliveryOperation{Operation: "publish", Work: d.work}, nil
}
func (d *activityDB) DeliveryOperationReceipt(context.Context, string, string) (string, error) {
	return d.receipt, nil
}
func (d *activityDB) CheckDeliveryWork(context.Context, string, string) (store.DeliveryWork, error) {
	d.checks++
	return d.work, nil
}
func (d *activityDB) CompleteDeliveryOperation(_ context.Context, _ string, _ string, o domain.DeliveryObservation) (string, error) {
	d.commits++
	d.receipt, _ = domain.JSONDigest(o)
	return d.receipt, nil
}

type activityPublisher struct {
	t     *testing.T
	calls int
}

func (p *activityPublisher) Publish(ctx context.Context, d domain.Delivery, a delivery.Prepared, check func(context.Context) error) (domain.DeliveryObservation, error) {
	p.calls++
	if err := check(ctx); err != nil {
		return domain.DeliveryObservation{}, err
	}
	if a.ResultTree != d.ResultTree || len(a.Files) != 1 || string(a.Files[0].Content) != "verified\n" {
		p.t.Fatal("publisher received unverified source")
	}
	return domain.DeliveryObservation{Tree: a.ResultTree, State: "draft", ObservedAt: time.Now().UTC()}, nil
}
func (p *activityPublisher) Observe(context.Context, domain.Delivery, func(context.Context) error) (domain.DeliveryObservation, error) {
	p.t.Fatal("unexpected observation")
	return domain.DeliveryObservation{}, nil
}
func TestActivityReconstructsBeforeCredentialsAndRecoversReceipt(t *testing.T) {
	bundle, patch := activityGit(t)
	binding := strings.Repeat("a", 32)
	ref := contextworkflow.Reference{ID: strings.Repeat("b", 32), Binding: binding}
	db := &activityDB{work: store.DeliveryWork{Binding: binding, Patch: patch, Delivery: domain.Delivery{ID: strings.Repeat("c", 32), ResultTree: patch.ResultTree}}}
	sourceCalls, credentialCalls := 0, 0
	activity, err := NewActivity(db, func(context.Context, string, string) ([]byte, error) { sourceCalls++; return bundle, nil }, func(context.Context, domain.DeliveryTarget, string) (string, error) {
		credentialCalls++
		return "synthetic-publication-credential", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &activityPublisher{t: t}
	activity.provider = func(domain.DeliveryTarget, string) (Publisher, error) { return provider, nil }
	result, err := activity.Publish(context.Background(), ref)
	if err != nil || result.ReceiptID != ref.ID || db.commits != 1 || provider.calls != 1 || credentialCalls != 1 || sourceCalls != 1 {
		t.Fatalf("activity result %+v %v", result, err)
	}
	again, err := activity.Publish(context.Background(), ref)
	if err != nil || again != result || provider.calls != 1 || sourceCalls != 1 || credentialCalls != 1 {
		t.Fatal("receipt retry repeated provider/source work")
	}
	db.receipt = ""
	db.work.Patch.ResultTree = strings.Repeat("f", 40)
	if _, err = activity.Publish(context.Background(), ref); err == nil || credentialCalls != 1 || provider.calls != 1 {
		t.Fatal("invalid artifact reached credentials/provider")
	}
}
