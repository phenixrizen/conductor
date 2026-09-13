// Package tui provides a terminal review workbench over the shared HTTP client.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Options selects local review inputs. Authenticated identity and canonical scope
// come from the configured client and server, never a local actor or content label.
type Options struct {
	Actor, Repository, File, ID string
}

// Run keeps the program alive until quit or cancellation; each HTTP operation has
// its own timeout so the CLI's usual single-command deadline does not end review.
func Run(ctx context.Context, c *client.Client, options Options) error {
	session, cancel := context.WithCancel(ctx)
	defer cancel()
	m, err := sessionModel(session, c, options)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithContext(session), tea.WithAltScreen()).Run()
	return err
}

func sessionModel(ctx context.Context, c *client.Client, options Options) (model, error) {
	if c == nil {
		return model{}, errors.New("terminal review requires an API client")
	}
	// The client retains public fields for existing callers. Snapshot those and
	// the HTTP configuration once so later caller edits cannot switch a session.
	fixed := *c
	if c.HTTP != nil {
		httpClient := *c.HTTP
		fixed.HTTP = &httpClient
	}
	workspace, repository, authenticated := fixed.AuthenticatedScope()
	if authenticated {
		if options.Actor != "" || fixed.Actor != "" {
			return model{}, errors.New("authenticated terminal identity comes from the server; a local actor cannot be combined with a token")
		}
		if domain.ValidateAccessID(workspace) != nil || domain.ValidateAccessID(repository) != nil {
			return model{}, errors.New("authenticated terminal review requires a workspace and canonical repository ID")
		}
	} else {
		if err := domain.ValidateActor(options.Actor); err != nil {
			return model{}, err
		}
		if options.Actor != fixed.Actor {
			return model{}, errors.New("terminal identity must match the HTTP client's local identity")
		}
	}
	if _, err := domain.ValidateChangeQuery(options.Repository, "", pageSize); err != nil {
		return model{}, err
	}
	m := newModel(options, runner(ctx, &fixed))
	m.access = accessState{authenticated: authenticated, workspaceID: workspace, repositoryID: repository}
	return m, nil
}

const pageSize = 20

type request struct {
	serial               int
	accessGeneration     int
	op, id, path, cursor string
	revision             int64
	digest               string
	content              domain.Content
	repository           string
}

type result struct {
	request
	page         domain.ChangePage
	pack         domain.Package
	content      domain.Content
	session      domain.Session
	repositories domain.RepositoryPage
	err          error
}

type executor func(request) (tea.Cmd, context.CancelFunc)

// The model owns cancel functions, not contexts. I/O commands capture their
// operation context and return immutable messages to Bubble Tea's update loop.
func runner(ctx context.Context, c *client.Client) executor {
	return func(req request) (tea.Cmd, context.CancelFunc) {
		operation, cancel := context.WithTimeout(ctx, 30*time.Second)
		return func() tea.Msg {
			defer cancel()
			res := result{request: req}
			switch req.op {
			case "access":
				res.session, res.err = c.Session(operation)
				if res.err == nil {
					res.repositories, res.err = c.Repositories(operation)
				}
			case "list":
				res.page, res.err = c.ListChanges(operation, req.repository, req.cursor, pageSize)
			case "open":
				res.pack, res.err = c.Get(operation, req.id)
			case "file":
				res.content, res.err = readContent(operation, req.path)
			case "create":
				res.pack, res.err = c.Create(operation, req.content)
			case "revise":
				res.pack, res.err = c.Revise(operation, req.id, req.revision, req.content)
			case "submit":
				res.pack, res.err = c.Submit(operation, req.id, req.revision)
			case "approve":
				// Approval intentionally sends only the tuple captured at confirmation.
				// Fetching here would silently approve content the reviewer never saw.
				res.pack, res.err = c.Approve(operation, req.id, req.revision, req.digest)
			default:
				res.err = errors.New("unknown terminal operation")
			}
			return res
		}, cancel
	}
}

func readContent(ctx context.Context, path string) (domain.Content, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" || path == "-" {
		return nil, errors.New("provide a regular JSON file; terminal input is reserved for review")
	}
	// Reject stable pipes/devices before opening. On Unix the nonblocking flag
	// also prevents a path swapped to a FIFO from hanging before descriptor
	// validation; the pre-open check alone is not a guarantee about the open file.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect content file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("package content must be a regular JSON file")
	}
	f, err := openContentFile(path)
	if err != nil {
		return nil, fmt.Errorf("open content file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read content file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("package file must contain at most 1 MiB")
	}
	var content domain.Content
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("decode content file: %w", err)
	}
	if err := domain.ValidateContent(content); err != nil {
		return nil, err
	}
	return content, nil
}

func openContentFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("package content must be a regular JSON file")
	}
	return f, nil
}
