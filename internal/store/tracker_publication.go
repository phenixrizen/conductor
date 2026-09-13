package store

import (
	"context"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

// Tracker publications retain immutable first-publication receipts and expose
// ongoing provider observations separately. Every run repository must already be
// represented by an exact package pin on the ticket relationship, so source
// dependencies cannot disappear from authorization or pre-pagination filtering.
func (p *Postgres) trackerPublications(ctx context.Context, l domain.TrackerLink) ([]domain.TrackerPublication, error) {
	result := []domain.TrackerPublication{}
	packages := map[string]domain.TrackerPackageRef{}
	repos := map[string]bool{}
	for _, ref := range l.Input.Packages {
		packages[ref.PackageID] = ref
		repos[ref.RepositoryID] = true
	}
	for _, ref := range l.Input.Publications {
		work, e := readDelivery(ctx, p.queries(), ref.ID)
		if e != nil {
			return nil, e
		}
		d := work.Delivery
		if d.WorkspaceID != l.WorkspaceID || d.Receipt == nil || d.Receipt.Digest != ref.Digest || !repos[d.RepositoryID] {
			return nil, domain.ErrNotFound
		}
		for _, source := range work.Plan.Repositories {
			if !repos[source.RepositoryID] {
				return nil, domain.ErrNotFound
			}
		}
		for _, pin := range work.Plan.Packages {
			stored, ok := packages[pin.ChangeID]
			if !ok || stored.RepositoryID != pin.RepositoryID || stored.Revision != pin.Revision || stored.Digest != pin.Digest {
				return nil, domain.ErrConflict
			}
		}
		scope := *p.access
		scope.request.RepositoryID = d.RepositoryID
		gate := &Postgres{pool: p.pool, tx: p.tx, access: &scope}
		if e = gate.authorizeDelivery(ctx, work, "read"); e != nil {
			return nil, e
		}
		result = append(result, domain.TrackerPublication{ID: d.ID, Digest: ref.Digest, Target: d.Target, Receipt: *d.Receipt, Observation: d.Observation, Current: d.Observation != nil && time.Since(d.Observation.ObservedAt) < 5*time.Minute})
	}
	return result, nil
}
