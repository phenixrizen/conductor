package acceptance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/pkg/client"
)

// This proves retained JSON transport, not live provider compatibility. The
// synthetic log objects include key orders and numbers that jsonb would change.
func TestAuthenticatedRuntimeReceiptPreservesSourceJSON(t *testing.T) {
	f := collectionFixture(t)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries().WithRuntimeEvidence(), f.verifier))
	input, c := seedRuntimeInput(t, f, domain.Content{"intent": "Synthetic retained source JSON"})
	r, err := c.CreateRuntimeEvidence(f.ctx, "runtime-exact-source", input)
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.RuntimeReceipt{CollectedAt: time.Now().UTC(), ProductionOutcome: "not_verified", Signals: []domain.RuntimeSignal{}, Evaluations: []domain.RuntimeEvaluation{}}
	for _, kind := range []string{"metrics", "logs", "traces"} {
		signal := domain.RuntimeSignal{Kind: kind, State: "missing", Correlation: "not_established", Coverage: "empty_query_result", QueryDigest: strings.Repeat("a", 64), ResponseDigest: strings.Repeat("b", 64), Endpoint: "/api/" + kind + "/v2/search", CollectedAt: receipt.CollectedAt, Series: []domain.RuntimeSeries{}, Records: []domain.RuntimeRecord{}}
		if kind == "metrics" {
			signal.Metric = "synthetic_latency"
			signal.Endpoint = "/api/metrics/query-range"
		} else {
			signal.State, signal.Correlation, signal.Coverage = "collected", "exact", "bounded_query_result"
			for _, data := range []string{`{"zz":9007199254740993,"a":[1e300,-0,{"large":9007199254740995,"q":1.25e+20}]}`, `{"nested":{"long":"synthetic","x":[0.00000000000000000001]}}`} {
				raw := json.RawMessage(fmt.Sprintf(`{"timestamp":%q,"service":"synthetic","env":"production","commit":%q,"payload":%s}`, input.End.Format(time.RFC3339), input.Commit, data))
				signal.Records = append(signal.Records, domain.RuntimeRecord{At: input.End, Data: raw})
			}
		}
		receipt.Signals = append(receipt.Signals, signal)
	}
	receipt.Digest, _ = domain.RuntimeReceiptDigest(receipt)
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM runtime_evidence WHERE id=$1`, r.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.CompleteRuntimeEvidence(f.ctx, r.ID, binding, receipt); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetRuntimeEvidence(f.ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(receipt)
	after, _ := json.Marshal(got.Receipt)
	if !bytes.Equal(before, after) {
		t.Fatal("typed API lost exact retained source representation")
	}
	digest, err := domain.RuntimeReceiptDigest(*got.Receipt)
	if err != nil || digest != receipt.Digest {
		t.Fatal("API receipt digest mismatch", err)
	}
	legacy, err := c.CreateRuntimeEvidence(f.ctx, "runtime-legacy-source", input)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CollectedAt = time.Now().UTC()
	for i := range receipt.Signals {
		receipt.Signals[i].CollectedAt = receipt.CollectedAt
	}
	receipt.Digest, _ = domain.RuntimeReceiptDigest(receipt)
	if _, err = f.sql.Exec(f.ctx, `INSERT INTO runtime_receipts(evidence_id,digest,receipt) VALUES($1,$2,($3::jsonb)::json)`, legacy.ID, receipt.Digest, receipt); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetRuntimeEvidence(f.ctx, legacy.ID)
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 || apiErr.Code != "service_unavailable" {
		t.Fatal("invalid historical receipt did not fail closed", err)
	}
	f.request("reviewer", "team", "private", "GET", "/api/v1/runtime-evidence/"+legacy.ID, nil, 404, nil)
}
