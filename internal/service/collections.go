package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/phenixrizen/conductor/internal/domain"
)

type collectionRepository interface {
	RequestCollection(context.Context, string, string, string, domain.CollectionInput) (domain.Collection, error)
	Collection(context.Context, string) (domain.Collection, error)
	Collections(context.Context, string, int) (domain.CollectionPage, error)
	CancelCollection(context.Context, string) (domain.Collection, error)
	AttachCollection(context.Context, string, int64, string, string) (domain.Package, error)
}

// WithCollections is an explicit server capability. Enabled repository records
// and per-command permissions are still checked inside PostgreSQL transactions.
func (s *AuthenticatedService) WithCollections() *AuthenticatedService {
	copy := *s
	copy.collections = true
	return &copy
}

func collectionCommand[T any](ctx context.Context, s *AuthenticatedService, run func(collectionRepository) (T, error)) (T, error) {
	var zero T
	if !s.collections {
		return zero, domain.ErrUnavailable
	}
	access, ok := domain.AccessFromContext(ctx)
	if !ok {
		return zero, domain.ErrUnauthenticated
	}
	// The initial collection workflow has a fixed canonical repository scope,
	// including discovery and historical receipt reads. No content label can
	// substitute for either server-owned selection.
	if access.WorkspaceID == "" || access.RepositoryID == "" {
		return zero, domain.ErrInvalidInput
	}
	return authenticated(ctx, s, func(v *Service, _ string) (T, error) {
		repo, ok := v.repo.(collectionRepository)
		if !ok {
			return zero, domain.ErrUnavailable
		}
		return run(repo)
	})
}

func (s *AuthenticatedService) CreateCollection(ctx context.Context, key string, input domain.CollectionInput) (domain.Collection, error) {
	if err := domain.ValidateCollectionKey(key); err != nil {
		return domain.Collection{}, err
	}
	in, err := domain.NormalizeCollectionInput(input)
	if err != nil {
		return domain.Collection{}, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return domain.Collection{}, err
	}
	return collectionCommand(ctx, s, func(repo collectionRepository) (domain.Collection, error) {
		return repo.RequestCollection(ctx, hex.EncodeToString(random[:16]), hex.EncodeToString(random[16:]), key, in)
	})
}
func (s *AuthenticatedService) GetCollection(ctx context.Context, id string) (domain.Collection, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.Collection{}, domain.ErrInvalidInput
	}
	return collectionCommand(ctx, s, func(repo collectionRepository) (domain.Collection, error) { return repo.Collection(ctx, id) })
}
func (s *AuthenticatedService) ListCollections(ctx context.Context, before string, limit int) (domain.CollectionPage, error) {
	if _, err := domain.ValidateChangeQuery("", before, limit); err != nil {
		return domain.CollectionPage{}, err
	}
	return collectionCommand(ctx, s, func(repo collectionRepository) (domain.CollectionPage, error) {
		return repo.Collections(ctx, before, limit)
	})
}
func (s *AuthenticatedService) CancelCollection(ctx context.Context, id string) (domain.Collection, error) {
	if !domain.IsLowerHex(id, 32) {
		return domain.Collection{}, domain.ErrInvalidInput
	}
	return collectionCommand(ctx, s, func(repo collectionRepository) (domain.Collection, error) { return repo.CancelCollection(ctx, id) })
}
func (s *AuthenticatedService) AttachCollection(ctx context.Context, changeID string, expected int64, id, digest string) (domain.Package, error) {
	if domain.ValidateAccessID(changeID) != nil || expected < 1 || !domain.IsLowerHex(id, 32) || !domain.IsLowerHex(digest, 64) {
		return domain.Package{}, domain.ErrInvalidInput
	}
	return collectionCommand(ctx, s, func(repo collectionRepository) (domain.Package, error) {
		return repo.AttachCollection(ctx, changeID, expected, id, digest)
	})
}
