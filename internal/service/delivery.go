package service

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
)

type deliveryRepository interface {
	DeliveryArtifact(context.Context, string) (domain.DeliveryArtifact, error)
	CreateDelivery(context.Context, string, domain.DeliveryInput) (domain.Delivery, error)
	Delivery(context.Context, string) (domain.Delivery, error)
	Deliveries(context.Context, string, int) (domain.DeliveryPage, error)
	AuthorizeDelivery(context.Context, string, string) (domain.Delivery, error)
	ReconcileDelivery(context.Context, string, string, string) (domain.Delivery, error)
}

// WithDeliveries exposes the operator-enabled publication capability. Provider
// bindings and distinct human publication grants remain transactional checks.
func (s *AuthenticatedService) WithDeliveries() *AuthenticatedService {
	copy := *s
	copy.deliveries = true
	return &copy
}
func deliveryCommand[T any](ctx context.Context, s *AuthenticatedService, run func(deliveryRepository) (T, error)) (T, error) {
	var zero T
	if !s.deliveries {
		return zero, domain.ErrUnavailable
	}
	access, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	if access.WorkspaceID == "" || access.RepositoryID == "" {
		return zero, domain.ErrInvalidInput
	}
	return authenticated(ctx, s, func(v *Service, _ string) (T, error) {
		repo, ok := v.repo.(deliveryRepository)
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return run(repo)
	})
}
func (s *AuthenticatedService) CreateDelivery(ctx context.Context, key string, input domain.DeliveryInput) (domain.Delivery, error) {
	if domain.ValidateCollectionKey(key) != nil || domain.ValidateDeliveryInput(input) != nil {
		return domain.Delivery{}, domain.ErrInvalidInput
	}
	return deliveryCommand(ctx, s, func(repo deliveryRepository) (domain.Delivery, error) { return repo.CreateDelivery(ctx, key, input) })
}
func (s *AuthenticatedService) GetDelivery(ctx context.Context, id string) (domain.Delivery, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.Delivery{}, domain.ErrInvalidInput
	}
	return deliveryCommand(ctx, s, func(repo deliveryRepository) (domain.Delivery, error) { return repo.Delivery(ctx, id) })
}
func (s *AuthenticatedService) ListDeliveries(ctx context.Context, before string, limit int) (domain.DeliveryPage, error) {
	if _, err := domain.ValidateChangeQuery("", before, limit); err != nil {
		return domain.DeliveryPage{}, err
	}
	return deliveryCommand(ctx, s, func(repo deliveryRepository) (domain.DeliveryPage, error) { return repo.Deliveries(ctx, before, limit) })
}
func (s *AuthenticatedService) AuthorizeDelivery(ctx context.Context, id, digest string) (domain.Delivery, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(digest, 64) {
		return domain.Delivery{}, domain.ErrInvalidInput
	}
	return deliveryCommand(ctx, s, func(repo deliveryRepository) (domain.Delivery, error) { return repo.AuthorizeDelivery(ctx, id, digest) })
}
func (s *AuthenticatedService) ReconcileDelivery(ctx context.Context, id, digest, key string) (domain.Delivery, error) {
	if !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(digest, 64) || domain.ValidateCollectionKey(key) != nil {
		return domain.Delivery{}, domain.ErrInvalidInput
	}
	return deliveryCommand(ctx, s, func(repo deliveryRepository) (domain.Delivery, error) {
		return repo.ReconcileDelivery(ctx, id, digest, key)
	})
}

func (s *AuthenticatedService) GetDeliveryArtifact(ctx context.Context, id string) (domain.DeliveryArtifact, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.DeliveryArtifact{}, domain.ErrInvalidInput
	}
	return deliveryCommand(ctx, s, func(repo deliveryRepository) (domain.DeliveryArtifact, error) { return repo.DeliveryArtifact(ctx, id) })
}
