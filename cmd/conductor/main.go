package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext"
	"github.com/phenixrizen/conductor/internal/tui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	if cmd == "assistant-config" {
		if err := runAssistantConfig(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
			if !errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
		}
		return
	}
	f := flag.NewFlagSet(cmd, flag.ExitOnError)
	actor := f.String("actor", "", "local development identity")
	workspace := f.String("workspace", env("CONDUCTOR_WORKSPACE", ""), "authenticated workspace ID")
	repositoryID := f.String("repository-id", env("CONDUCTOR_REPOSITORY_ID", ""), "canonical managed repository ID for authenticated requests")
	title := f.String("title", "", "package title")
	file := f.String("file", "", "JSON content or request file; request previews require a regular file")
	view := f.String("view", "", "terminal view: graphs, runs, deliveries, tracker or runtime (default package review)")
	search := f.String("search", "", "bounded graph text search")
	node := f.String("node", "", "inspected graph node ID for traversal")
	depth := f.Int("depth", 1, "graph traversal depth 0-5")
	rev := f.Int64("revision", 0, "inspected revision")
	digest := f.String("digest", "", "inspected digest")
	taskID := f.String("task-id", "", "inspected opaque coordination task ID")
	artifactDigest := f.String("artifact-digest", "", "inspected retained coordination artifact digest")
	before := f.Int64("before", 0, "history cursor: revisions before this number")
	page := f.String("page", "", "opaque continuation cursor for shared changes or context collections")
	after := f.Int64("after", 0, "audit cursor: events after this sequence")
	limit := f.Int("limit", 20, "list, history, or audit page size (1-100)")
	repo := f.String("repo", ".", "local Git repository for context collection/check")
	repository := f.String("repository", "", "repository identity recorded in the snapshot")
	ref := f.String("ref", "HEAD", "Git ref to resolve to a commit")
	commit := f.String("commit", "", "full commit object ID for remote context collection")
	fullSource := f.Bool("full-source", false, "also acquire an exact Git bundle and whole-tree CodeGraph index")
	idempotencyKey := f.String("idempotency-key", "", "stable key for an explicitly requested remote collection")
	collectionID := f.String("collection-id", "", "inspected remote collection ID for attachment")
	sourceRepository := f.String("source-repository", "", "canonical graph source repository ID; the authenticated anchor stays fixed")
	receiptDigest := f.String("receipt-digest", "", "inspected graph source receipt digest")
	fullSourceDigest := f.String("full-source-digest", "", "inspected whole-source graph bundle digest")
	expectedRevision := f.Int64("expected-revision", 0, "inspected package revision for context attachment")
	var paths []string
	f.Func("path", "explicit repository-relative artifact path (repeatable)", func(path string) error {
		paths = append(paths, path)
		return nil
	})
	_ = f.Parse(os.Args[2:])
	*actor = strings.TrimSpace(*actor)
	actorSet := false
	f.Visit(func(option *flag.Flag) {
		if option.Name == "actor" {
			actorSet = true
		}
	})
	c, clientErr := clientForCommand(cmd, *actor, actorSet, *workspace, *repositoryID)
	if clientErr != nil {
		fmt.Fprintln(os.Stderr, clientErr)
		os.Exit(2)
	}
	if cmd == "tui" {
		id := ""
		if len(f.Args()) > 0 {
			id = f.Args()[0]
		}
		if err := tui.Run(context.Background(), c, tui.Options{Actor: *actor, Repository: *repository, File: *file, ID: id, View: *view}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var p any
	var err error
	exitCode := 0
	args := f.Args()
	switch {
	case releaseCommand(cmd):
		artifactPath := ""
		if len(paths) == 1 {
			artifactPath = paths[0]
		}
		p, err = runReleaseCommand(ctx, c, cmd, args, releaseOptions{taskArtifact: domain.CoordinationArtifactQuery{RunDigest: *digest, TaskID: *taskID, ArtifactDigest: *artifactDigest}, file: *file, digest: *digest, key: *idempotencyKey, before: *page, limit: *limit, search: *search, node: *node, depth: *depth, artifact: domain.GraphArtifactQuery{GraphDigest: *digest, RepositoryID: *sourceRepository, CollectionID: *collectionID, ReceiptDigest: *receiptDigest, FullSourceDigest: *fullSourceDigest, Path: artifactPath}})
	default:
		switch cmd {
		case "context-collect", "context-collections", "context-collection", "context-cancel", "context-attach":
			p, err = runCollectionCommand(ctx, c, cmd, args, collectionOptions{
				commit: *commit, paths: paths, key: *idempotencyKey, fullSource: *fullSource, before: *page, limit: *limit,
				collectionID: *collectionID, digest: *digest, expectedRevision: *expectedRevision,
			})
		case "session":
			p, err = c.Session(ctx)
		case "repositories":
			p, err = c.Repositories(ctx)
		case "list":
			p, err = c.ListChanges(ctx, *repository, *page, *limit)
		case "create":
			content, readErr := contentFrom(*file, *title)
			if readErr != nil {
				err = readErr
				break
			}
			p, err = c.Create(ctx, content)
		case "revise":
			need(args)
			content, readErr := contentFrom(*file, *title)
			if readErr != nil {
				err = readErr
				break
			}
			p, err = c.Revise(ctx, args[0], *rev, content)
		case "show":
			need(args)
			if *rev != 0 {
				p, err = c.Revision(ctx, args[0], *rev)
			} else {
				p, err = c.Get(ctx, args[0])
			}
		case "history":
			need(args)
			p, err = c.History(ctx, args[0], *before, *limit)
		case "events":
			need(args)
			p, err = c.Events(ctx, args[0], *after, *limit)
		case "submit":
			need(args)
			p, err = c.Submit(ctx, args[0], *rev)
		case "approve":
			need(args)
			p, err = c.Approve(ctx, args[0], *rev, *digest)
		case "context":
			content, readErr := contentFrom(*file, *title)
			if readErr != nil {
				err = readErr
				break
			}
			var snapshot domain.RepositoryContext
			snapshot, err = repositorycontext.Collect(ctx, *repo, *repository, *ref, paths)
			if err == nil {
				content["repositoryContext"] = snapshot
				err = domain.ValidateContent(content)
				p = content
			}
		case "context-check":
			content, readErr := contentFrom(*file, "")
			if readErr != nil {
				err = readErr
				break
			}
			var snapshot domain.RepositoryContext
			snapshot, err = snapshotFrom(content)
			if err == nil {
				freshness := repositorycontext.CheckFreshness(ctx, *repo, *ref, snapshot)
				p = freshness
				if freshness.State != "current" {
					exitCode = 1
				}
			}
		default:
			usage()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
func need(a []string) {
	if len(a) == 0 {
		usage()
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func usage() {
	fmt.Fprintln(os.Stderr, `usage: conductor <command> [flags] [id]
Native assistance: assistant-config
Review: tui, session, repositories, list, create, revise, show, history, events, submit, approve
Context: context, context-check, context-collect, context-collections, context-collection, context-cancel, context-attach
Graphs: graphs, graph, graph-query, graph-artifact, graph-preview, graph-create
Agent work: runs, run, run-artifact, run-preview, run-propose, run-authorize, run-cancel, execution-profiles, execution-capabilities
Delivery: deliveries, delivery, delivery-preview, delivery-propose, delivery-artifact, delivery-authorize, delivery-reconcile
Runtime: runtime-evidence, runtime, runtime-preview, runtime-collect
Tracker: tracker, tracker-links, tracker-link, tracker-link-preview, tracker-link-create, tracker-sync-preview, tracker-sync, tracker-sync-show
Use --help after a command for flags. Release request files and controls: docs/operations/release-terminal.md`)
	os.Exit(2)
}

func contentFrom(path, title string) (domain.Content, error) {
	if path == "" {
		if title == "" {
			return nil, errors.New("--title or --file is required")
		}
		return domain.Content{"intent": map[string]any{"title": title}}, nil
	}
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read package content: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, errors.New("package file must contain at most 1 MiB")
	}
	var content domain.Content
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("read package content: %w", err)
	}
	if content == nil {
		return nil, errors.New("package content must be a JSON object")
	}
	return content, nil
}

func snapshotFrom(content domain.Content) (domain.RepositoryContext, error) {
	var snapshot domain.RepositoryContext
	value, ok := content["repositoryContext"]
	if !ok {
		return snapshot, errors.New("package has no repositoryContext snapshot")
	}
	if err := domain.ValidateRepositoryContext(value); err != nil {
		return snapshot, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return snapshot, err
	}
	err = json.Unmarshal(data, &snapshot)
	return snapshot, err
}
