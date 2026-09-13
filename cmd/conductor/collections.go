package main

import (
	"context"
	"errors"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

type collectionOptions struct {
	commit, key, before, collectionID, digest string
	paths                                     []string
	limit                                     int
	expectedRevision                          int64
	fullSource                                bool
}

func collectionCommand(command string) bool {
	switch command {
	case "context-collect", "context-collections", "context-collection", "context-cancel", "context-attach":
		return true
	}
	return false
}

func runCollectionCommand(ctx context.Context, c *client.Client, command string, args []string, options collectionOptions) (any, error) {
	switch command {
	case "context-collect":
		if len(args) != 0 || options.commit == "" || len(options.paths) == 0 || options.key == "" {
			return nil, errors.New("context-collect requires --commit, one or more --path flags, and --idempotency-key; no positional arguments")
		}
		return c.CreateCollection(ctx, options.key, domain.CollectionInput{Commit: options.commit, Paths: options.paths, FullSource: options.fullSource})
	case "context-collections":
		if len(args) != 0 || options.limit < 1 || options.limit > domain.MaxHistoryPageSize {
			return nil, errors.New("context-collections accepts --page and --limit 1-100; no positional arguments")
		}
		return c.ListCollections(ctx, options.before, options.limit)
	case "context-collection", "context-cancel":
		if len(args) != 1 || args[0] == "" {
			return nil, errors.New("context-collection and context-cancel require exactly one collection ID after the flags")
		}
		if command == "context-cancel" {
			return c.CancelCollection(ctx, args[0])
		}
		return c.GetCollection(ctx, args[0])
	case "context-attach":
		if len(args) != 1 || args[0] == "" || options.collectionID == "" || options.digest == "" || options.expectedRevision < 1 {
			return nil, errors.New("context-attach requires --collection-id, --digest, --expected-revision, and exactly one change ID after the flags")
		}
		return c.AttachCollection(ctx, args[0], options.expectedRevision, options.collectionID, options.digest)
	default:
		return nil, errors.New("unknown remote context command")
	}
}
