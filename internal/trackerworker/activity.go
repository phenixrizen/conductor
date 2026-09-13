// Package trackerworker owns provider I/O for durable tracker synchronization.
// Workflow history contains opaque references and receipt digests only.
package trackerworker

import (
	"context"
	"errors"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/internal/tracker"
	"github.com/phenixrizen/conductor/internal/trackerworkflow"
)

type WorkStore interface {
	TrackerWork(context.Context, string, string) (store.TrackerWork, error)
	CheckTrackerWork(context.Context, string, string) error
	BeginTrackerWrite(context.Context, string, string) (bool, error)
	CompleteTrackerSync(context.Context, string, string, domain.TrackerObservation) (domain.TrackerObservation, error)
}
type Provider interface {
	Read(context.Context, string, string) (domain.TrackerIssue, *domain.TrackerProjection, error)
	Write(context.Context, string, domain.TrackerProjection) error
}
type Credentials func(context.Context, domain.TrackerConfig, string) (tracker.Credential, error)
type Factory func(domain.TrackerConfig, tracker.Credential, func(context.Context) error) (Provider, error)
type Activity struct {
	db          WorkStore
	credentials Credentials
	factory     Factory
}

func NewActivity(db WorkStore, credentials Credentials, factory Factory) (*Activity, error) {
	if db == nil || credentials == nil {
		return nil, domain.ErrInvalidInput
	}
	if factory == nil {
		factory = func(c domain.TrackerConfig, k tracker.Credential, check func(context.Context) error) (Provider, error) {
			return tracker.New(c, k, check, tracker.Options{})
		}
	}
	return &Activity{db, credentials, factory}, nil
}
func (a *Activity) Sync(ctx context.Context, ref trackerworkflow.Reference) (trackerworkflow.Result, error) {
	w, e := a.db.TrackerWork(ctx, ref.ID, ref.Binding)
	if e != nil {
		return trackerworkflow.Result{}, errors.New("tracker work unavailable")
	}
	receipt := func(o domain.TrackerObservation) (trackerworkflow.Result, error) {
		return trackerworkflow.Result{ID: ref.ID, Digest: domain.TrackerDigest(o)}, nil
	}
	if w.Sync.Observation != nil {
		o := *w.Sync.Observation
		o.Current = false
		return receipt(o)
	}
	finish := func(o domain.TrackerObservation) (trackerworkflow.Result, error) {
		saved, e := a.db.CompleteTrackerSync(ctx, ref.ID, ref.Binding, o)
		if e != nil {
			return trackerworkflow.Result{}, errors.New("tracker receipt unavailable")
		}
		return receipt(saved)
	}
	unavailable := func(code string) (trackerworkflow.Result, error) {
		return finish(domain.TrackerObservation{State: "unavailable", Code: code})
	}
	check := func(ctx context.Context) error { return a.db.CheckTrackerWork(ctx, ref.ID, ref.Binding) }
	if e = check(ctx); e != nil {
		return unavailable("access_or_binding_changed")
	}
	credential, e := a.credentials(ctx, w.Config, w.Config.CredentialID)
	if e != nil {
		return unavailable("credential_unavailable")
	}
	provider, e := a.factory(w.Config, credential, check)
	if e != nil {
		return unavailable("configuration_unavailable")
	}
	issue, projection, e := provider.Read(ctx, w.Link.Input.IssueID, w.Link.Projection.URL)
	if e != nil {
		return unavailable("provider_read_unavailable")
	}
	observed := domain.TrackerObservation{State: "refreshed", Issue: &issue, Projection: projection, ProjectionDigest: tracker.ProjectionDigest(projection)}
	expected := domain.TrackerDigest(w.Link.Projection)
	if issue.Mapping == "unmapped" {
		observed.State = "conflict"
		observed.Code = "unmapped_status"
		return finish(observed)
	}
	if observed.ProjectionDigest == expected {
		observed.State = "synchronized"
		return finish(observed)
	}
	if w.Sync.Input.Mode == "refresh" {
		if projection != nil {
			observed.State = "conflict"
			observed.Code = "projection_changed"
		}
		return finish(observed)
	}
	if w.WriteAttempted {
		observed.State = "unknown"
		observed.Code = "prior_write_unconfirmed"
		return finish(observed)
	}
	if observed.ProjectionDigest != w.Sync.Input.ExpectedProjectionDigest || projection != nil && w.Sync.Input.Mode != "restore" {
		observed.State = "conflict"
		observed.Code = "projection_changed"
		return finish(observed)
	}
	attempted, e := a.db.BeginTrackerWrite(ctx, ref.ID, ref.Binding)
	if e != nil {
		return unavailable("write_authority_changed")
	}
	if !attempted {
		observed.State = "unknown"
		observed.Code = "prior_write_unconfirmed"
		return finish(observed)
	}
	// The durable write fact is committed before transmission. A retry may inspect
	// the unique remote card, but can never blindly transmit this mutation again.
	_ = provider.Write(ctx, w.Link.Input.IssueID, w.Link.Projection)
	issue, projection, e = provider.Read(ctx, w.Link.Input.IssueID, w.Link.Projection.URL)
	if e != nil {
		return finish(domain.TrackerObservation{State: "unknown", Code: "write_unconfirmed"})
	}
	observed.Issue = &issue
	observed.Projection = projection
	observed.ProjectionDigest = tracker.ProjectionDigest(projection)
	observed.State = "unknown"
	observed.Code = "write_unconfirmed"
	if observed.ProjectionDigest == expected {
		observed.State = "synchronized"
		observed.Code = ""
	}
	result, e := finish(observed)
	if e != nil {
		return unavailable("commit_authority_changed")
	}
	return result, nil
}
