package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/runtimeevidence"
)

func runtimeSourceReceipt(r domain.RuntimeEvidence) domain.RuntimeReceipt {
	receipt := runtimeReceiptFixture(r)
	for i := range receipt.Signals {
		signal := &receipt.Signals[i]
		if signal.Kind == "metrics" {
			continue
		}
		signal.State, signal.Correlation, signal.Coverage = "collected", "exact", "bounded_query_result"
		for _, nested := range []string{`{"zz":9007199254740993,"a":[1e300,-0,0.0000000000000000001,{"long":true,"x":null}]}`, `{"a":{"z":1.25e+20,"bb":"synthetic"},"zz":9007199254740995}`} {
			raw := json.RawMessage(fmt.Sprintf(`{"timestamp":%q,"service":%q,"env":%q,"commit":%q,"nested":%s}`, r.Input.End.Format("2006-01-02T15:04:05Z"), r.Target.Service, r.Input.Environment, r.Input.Commit, nested))
			signal.Records = append(signal.Records, domain.RuntimeRecord{At: r.Input.End, Data: raw})
		}
	}
	receipt.Evaluations = runtimeevidence.Evaluate(r, receipt)
	receipt.Digest, _ = domain.RuntimeReceiptDigest(receipt)
	return receipt
}

func TestRuntimeReceiptJSONRetainsExactNestedSource(t *testing.T) {
	ctx, p, s, input, _ := runtimeFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	r, err := s.CreateRuntimeEvidence(human, "exact-json", input)
	if err != nil {
		t.Fatal(err)
	}
	var binding string
	if err = p.pool.QueryRow(ctx, `SELECT binding FROM runtime_evidence WHERE id=$1`, r.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	receipt := runtimeSourceReceipt(r)
	if _, err = p.CompleteRuntimeEvidence(ctx, r.ID, binding, receipt); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRuntimeEvidence(human, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(receipt)
	after, _ := json.Marshal(got.Receipt)
	if !bytes.Equal(before, after) {
		t.Fatal("PostgreSQL changed bounded source bytes or numeric precision")
	}
	digest, err := domain.RuntimeReceiptDigest(*got.Receipt)
	if err != nil || digest != receipt.Digest {
		t.Fatal("retained receipt digest changed", err)
	}
	if digest, err = p.RuntimeOperationReceipt(ctx, r.ID, binding); err != nil || digest != receipt.Digest {
		t.Fatal("reconciliation lost exact receipt", err)
	}
	if err = p.CheckRuntimeSchema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLegacyJSONBReceiptFailsClosedWithoutRewritingHistory(t *testing.T) {
	ctx, p, s, input, _ := runtimeFixture(t)
	human := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	r, err := s.CreateRuntimeEvidence(human, "legacy-jsonb", input)
	if err != nil {
		t.Fatal(err)
	}
	var binding string
	if err = p.pool.QueryRow(ctx, `SELECT binding FROM runtime_evidence WHERE id=$1`, r.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	// Recreate the previous storage type only in this isolated owned schema.
	if _, err = p.pool.Exec(ctx, `ALTER TABLE runtime_receipts ALTER COLUMN receipt TYPE jsonb USING receipt::jsonb`); err != nil {
		t.Fatal(err)
	}
	if err = p.CheckRuntimeSchema(ctx); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("old startup schema accepted", err)
	}
	receipt := runtimeSourceReceipt(r)
	if _, err = p.pool.Exec(ctx, `INSERT INTO runtime_receipts(evidence_id,digest,receipt) VALUES($1,$2,$3)`, r.ID, receipt.Digest, receipt); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = p.pool.QueryRow(ctx, `SELECT receipt::text FROM runtime_receipts WHERE evidence_id=$1`, r.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/011_runtime_receipt_json.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var after, digest string
	if err = p.pool.QueryRow(ctx, `SELECT receipt::text,digest FROM runtime_receipts WHERE evidence_id=$1`, r.ID).Scan(&after, &digest); err != nil {
		t.Fatal(err)
	}
	if before != after || digest != receipt.Digest {
		t.Fatal("migration rewrote historical source or digest")
	}
	if _, err = s.GetRuntimeEvidence(human, r.ID); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("legacy mismatch rendered as evidence", err)
	}
	if _, err = s.GetRuntimeEvidence(accessContext(ctx, "reviewer", "workspace-one", "repo-two"), r.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("legacy mismatch leaked before scope authorization", err)
	}
	if _, err = p.RuntimeOperationReceipt(ctx, r.ID, binding); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("legacy mismatch completed workflow", err)
	}
	if _, err = p.CompleteRuntimeEvidence(ctx, r.ID, binding, receipt); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("retry rewrote legacy evidence", err)
	}
}
