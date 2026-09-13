package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/phenixrizen/conductor/internal/domain"
)

type trackerRepository interface {
	TrackerSettings(context.Context) (domain.TrackerSettings, error)
	CreateTrackerLink(context.Context, string, string, domain.TrackerLinkInput) (domain.TrackerLink, error)
	TrackerLink(context.Context, string) (domain.TrackerLink, error)
	TrackerLinks(context.Context, string, int) (domain.TrackerLinkPage, error)
	RequestTrackerSync(context.Context, string, string, string, domain.TrackerSyncInput) (domain.TrackerSync, error)
	TrackerSync(context.Context, string) (domain.TrackerSync, error)
}

func trackerCommand[T any](ctx context.Context, s *AuthenticatedService, f func(trackerRepository) (T, error)) (T, error) {
	return authenticated(ctx, s, func(v *Service, _ string) (T, error) {
		r, ok := v.repo.(trackerRepository)
		if !ok {
			var zero T
			return zero, domain.ErrUnavailable
		}
		return f(r)
	})
}
func trackerID() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func (s *AuthenticatedService) GetTracker(ctx context.Context) (domain.TrackerSettings, error) {
	return trackerCommand(ctx, s, func(r trackerRepository) (domain.TrackerSettings, error) { return r.TrackerSettings(ctx) })
}
func (s *AuthenticatedService) CreateTrackerLink(ctx context.Context, key string, in domain.TrackerLinkInput) (domain.TrackerLink, error) {
	var zero domain.TrackerLink
	if domain.ValidateCollectionKey(key) != nil {
		return zero, domain.ErrInvalidInput
	}
	in, e := domain.NormalizeTrackerLink(in)
	if e != nil {
		return zero, e
	}
	id, e := trackerID()
	if e != nil {
		return zero, e
	}
	return trackerCommand(ctx, s, func(r trackerRepository) (domain.TrackerLink, error) { return r.CreateTrackerLink(ctx, id, key, in) })
}
func (s *AuthenticatedService) GetTrackerLink(ctx context.Context, id string) (domain.TrackerLink, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.TrackerLink{}, domain.ErrInvalidInput
	}
	return trackerCommand(ctx, s, func(r trackerRepository) (domain.TrackerLink, error) { return r.TrackerLink(ctx, id) })
}
func (s *AuthenticatedService) ListTrackerLinks(ctx context.Context, before string, limit int) (domain.TrackerLinkPage, error) {
	if before != "" && !domain.IsLowerHex(before, 32) || limit < 1 || limit > 100 {
		return domain.TrackerLinkPage{}, domain.ErrInvalidInput
	}
	return trackerCommand(ctx, s, func(r trackerRepository) (domain.TrackerLinkPage, error) { return r.TrackerLinks(ctx, before, limit) })
}
func (s *AuthenticatedService) RequestTrackerSync(ctx context.Context, linkID, key string, in domain.TrackerSyncInput) (domain.TrackerSync, error) {
	var zero domain.TrackerSync
	if !domain.IsLowerHex(linkID, 32) || domain.ValidateCollectionKey(key) != nil || domain.ValidateTrackerSync(in) != nil {
		return zero, domain.ErrInvalidInput
	}
	id, e := trackerID()
	if e != nil {
		return zero, e
	}
	return trackerCommand(ctx, s, func(r trackerRepository) (domain.TrackerSync, error) {
		return r.RequestTrackerSync(ctx, id, linkID, key, in)
	})
}
func (s *AuthenticatedService) GetTrackerSync(ctx context.Context, id string) (domain.TrackerSync, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.TrackerSync{}, domain.ErrInvalidInput
	}
	return trackerCommand(ctx, s, func(r trackerRepository) (domain.TrackerSync, error) { return r.TrackerSync(ctx, id) })
}
