package delivery

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type gitlabCommit struct {
	ID      string   `json:"id"`
	Message string   `json:"message"`
	Parents []string `json:"parent_ids"`
}
type gitlabBranch struct {
	Name   string       `json:"name"`
	Commit gitlabCommit `json:"commit"`
}
type gitlabMR struct {
	ID           int64      `json:"id"`
	IID          int64      `json:"iid"`
	ProjectID    int64      `json:"project_id"`
	SourceID     int64      `json:"source_project_id"`
	TargetID     int64      `json:"target_project_id"`
	SourceBranch string     `json:"source_branch"`
	TargetBranch string     `json:"target_branch"`
	SHA          string     `json:"sha"`
	Description  string     `json:"description"`
	URL          string     `json:"web_url"`
	State        string     `json:"state"`
	Draft        bool       `json:"draft"`
	MergedAt     *time.Time `json:"merged_at"`
	MergeCommit  string     `json:"merge_commit_sha"`
}
type gitlabTreeEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
}

func (p *Provider) gitlabBranch(ctx context.Context, b *callBudget, branch string) (string, error) {
	var found gitlabBranch
	if _, err := b.call(ctx, "GET", "/repository/branches/"+url.PathEscape(branch), nil, &found); err != nil {
		return "", err
	}
	if found.Name != branch || !domain.IsLowerHex(found.Commit.ID, 40) {
		return "", ErrMismatch
	}
	return found.Commit.ID, nil
}
func (p *Provider) gitlabExactCommit(ctx context.Context, b *callBudget, d domain.Delivery, sha string) error {
	var commit gitlabCommit
	if _, err := b.call(ctx, "GET", "/repository/commits/"+sha, nil, &commit); err != nil {
		return err
	}
	if commit.ID != sha || len(commit.Parents) != 1 || commit.Parents[0] != d.BaseCommit || commit.Message != commitMessage(d) {
		return ErrMismatch
	}
	// GitLab's commit DTO omits its root tree. Reconstruct its Git object hash from
	// every root entry (including subtree IDs), without downloading source text.
	entries := []gitlabTreeEntry{}
	page := 1
	for {
		var values []gitlabTreeEntry
		query := url.Values{"ref": {sha}, "recursive": {"false"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		headers, err := b.call(ctx, "GET", "/repository/tree?"+query.Encode(), nil, &values)
		if err != nil {
			return err
		}
		if len(values) > 100 || len(entries)+len(values) > 10000 {
			return ErrProvider
		}
		entries = append(entries, values...)
		next := headers.Get("X-Next-Page")
		if next == "" {
			break
		}
		value, err := strconv.Atoi(next)
		if err != nil || value != page+1 || page >= 100 {
			return ErrProvider
		}
		page = value
	}
	tree, err := gitlabRootTree(entries)
	if err != nil || tree != d.ResultTree {
		return ErrMismatch
	}
	return nil
}
func gitlabRootTree(entries []gitlabTreeEntry) (string, error) {
	entries = append([]gitlabTreeEntry(nil), entries...)
	seen := map[string]bool{}
	for _, entry := range entries {
		if !safePath(entry.Name) || strings.Contains(entry.Name, "/") || entry.Name != entry.Path || seen[entry.Name] || !domain.IsLowerHex(entry.ID, 40) {
			return "", ErrMismatch
		}
		seen[entry.Name] = true
		if entry.Type == "tree" {
			if entry.Mode != "040000" && entry.Mode != "40000" {
				return "", ErrMismatch
			}
		} else if entry.Type != "blob" || entry.Mode != "100644" && entry.Mode != "100755" {
			return "", ErrMismatch
		}
	}
	key := func(entry gitlabTreeEntry) string {
		if entry.Type == "tree" {
			return entry.Name + "/"
		}
		return entry.Name
	}
	sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
	var body bytes.Buffer
	for _, entry := range entries {
		mode := entry.Mode
		if entry.Type == "tree" {
			mode = "40000"
		}
		_, _ = fmt.Fprintf(&body, "%s %s%c", mode, entry.Name, byte(0))
		oid, _ := hex.DecodeString(entry.ID)
		_, _ = body.Write(oid)
	}
	hash := sha1.New()
	_, _ = fmt.Fprintf(hash, "tree %d%c", body.Len(), byte(0))
	_, _ = hash.Write(body.Bytes())
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (p *Provider) publishGitLab(ctx context.Context, b *callBudget, d domain.Delivery, a Prepared) (domain.DeliveryObservation, error) {
	var zero domain.DeliveryObservation
	base, err := p.gitlabBranch(ctx, b, d.Input.BaseBranch)
	if err != nil {
		return zero, err
	}
	if base != d.BaseCommit {
		return zero, ErrMismatch
	}
	commit, err := p.gitlabBranch(ctx, b, d.Branch)
	if errors.Is(err, ErrMissing) {
		actions := []map[string]any{}
		for _, file := range a.Files {
			if file.Mode == "000000" {
				actions = append(actions, map[string]any{"action": "delete", "file_path": file.Path, "last_commit_id": d.BaseCommit})
				continue
			}
			action := "update"
			if file.OldMode == "000000" {
				action = "create"
			}
			values := map[string]any{"action": action, "file_path": file.Path, "content": base64.StdEncoding.EncodeToString(file.Content), "encoding": "base64"}
			if action == "update" {
				values["last_commit_id"] = d.BaseCommit
			}
			actions = append(actions, values)
			if file.OldMode != file.Mode && (file.Mode == "100755" || file.OldMode == "100755") {
				actions = append(actions, map[string]any{"action": "chmod", "file_path": file.Path, "execute_filemode": file.Mode == "100755"})
			}
		}
		var created gitlabCommit
		// An explicit start_sha and force=false mean a new branch only. Retries never
		// append to a pre-existing branch; they inspect exact commit/tree provenance.
		if _, err = b.call(ctx, "POST", "/repository/commits", map[string]any{"branch": d.Branch, "start_sha": d.BaseCommit, "force": false, "commit_message": commitMessage(d), "author_name": "Conductor", "author_email": "conductor@example.invalid", "actions": actions}, &created); err != nil {
			return zero, err
		}
		if !domain.IsLowerHex(created.ID, 40) {
			return zero, ErrMismatch
		}
		commit = created.ID
	} else if err != nil {
		return zero, err
	}
	if err = p.gitlabExactCommit(ctx, b, d, commit); err != nil {
		return zero, err
	}
	base, err = p.gitlabBranch(ctx, b, d.Input.BaseBranch)
	if err != nil {
		return zero, err
	}
	if base != d.BaseCommit {
		return zero, ErrMismatch
	}
	query := url.Values{"scope": {"all"}, "state": {"all"}, "source_branch": {d.Branch}, "target_branch": {d.Input.BaseBranch}, "per_page": {"100"}}
	var requests []gitlabMR
	headers, err := b.call(ctx, "GET", "/merge_requests?"+query.Encode(), nil, &requests)
	if err != nil {
		return zero, err
	}
	if len(requests) > 1 || headers.Get("X-Next-Page") != "" {
		return zero, ErrMismatch
	}
	var number int64
	if len(requests) == 1 {
		if !strings.Contains(requests[0].Description, marker(d)) {
			return zero, ErrMismatch
		}
		number = requests[0].IID
	} else {
		var request gitlabMR
		if _, err = b.call(ctx, "POST", "/merge_requests", map[string]any{"source_branch": d.Branch, "target_branch": d.Input.BaseBranch, "title": "Draft: " + d.Input.Title, "description": body(d), "remove_source_branch": false, "allow_collaboration": false}, &request); err != nil {
			return zero, err
		}
		if !request.Draft {
			return zero, ErrMismatch
		}
		number = request.IID
	}
	return p.observeGitLab(ctx, b, d, number, commit)
}
func (p *Provider) observeGitLab(ctx context.Context, b *callBudget, d domain.Delivery, number int64, commit string) (domain.DeliveryObservation, error) {
	out := observation(d)
	if number < 1 || !domain.IsLowerHex(commit, 40) {
		return out, ErrMismatch
	}
	var request gitlabMR
	if _, err := b.call(ctx, "GET", "/merge_requests/"+strconv.FormatInt(number, 10), nil, &request); err != nil {
		return out, err
	}
	if request.IID != number || request.ID < 1 || request.SourceBranch != d.Branch || request.TargetBranch != d.Input.BaseBranch || request.SHA != commit || strconv.FormatInt(request.SourceID, 10) != p.target.ProviderID || strconv.FormatInt(request.TargetID, 10) != p.target.ProviderID || strconv.FormatInt(request.ProjectID, 10) != p.target.ProviderID || !validURL(request.URL, p.target.Host) {
		return out, ErrMismatch
	}
	if err := p.gitlabExactCommit(ctx, b, d, commit); err != nil {
		return out, err
	}
	out.ProviderID, out.Number, out.URL, out.Commit, out.Draft = strconv.FormatInt(request.ID, 10), number, request.URL, commit, request.Draft
	switch {
	case request.State == "merged" && request.MergedAt != nil && domain.IsLowerHex(request.MergeCommit, 40):
		out.State = "merged"
		out.MergeCommit = request.MergeCommit
	case request.State == "closed" && request.MergedAt == nil:
		out.State = "closed"
	case request.State == "opened" && request.MergedAt == nil:
		out.State = "open"
		if request.Draft {
			out.State = "draft"
		}
	default:
		return out, ErrMismatch
	}
	p.gitlabChecks(ctx, b, &out, d.Branch)
	p.gitlabDeployments(ctx, b, &out)
	return out, nil
}
func (p *Provider) gitlabChecks(ctx context.Context, b *callBudget, out *domain.DeliveryObservation, branch string) {
	var pipelines []struct {
		ID     int64  `json:"id"`
		SHA    string `json:"sha"`
		Status string `json:"status"`
		Ref    string `json:"ref"`
	}
	query := url.Values{"sha": {out.Commit}, "ref": {branch}, "order_by": {"id"}, "sort": {"desc"}, "per_page": {"100"}}
	headers, err := b.call(ctx, "GET", "/pipelines?"+query.Encode(), nil, &pipelines)
	if err != nil {
		out.ChecksState = "unavailable"
		return
	}
	if len(pipelines) > 100 || headers.Get("X-Next-Page") != "" {
		out.ChecksTruncated = true
	}
	// Pipelines can include independent workflows at one SHA. Retain each observed
	// status; an empty page or skipped pipeline cannot establish passing checks.
	for _, pipeline := range pipelines[:min(len(pipelines), 100)] {
		if pipeline.ID < 1 || pipeline.SHA != out.Commit || pipeline.Ref != branch {
			out.ChecksState = "unavailable"
			return
		}
		state := "unknown"
		switch pipeline.Status {
		case "success":
			state = "passed"
		case "failed", "canceled":
			state = "failed"
		case "pending", "running", "created", "waiting_for_resource", "preparing":
			state = "pending"
		case "skipped", "manual":
			state = "unexecuted"
		}
		out.Checks = append(out.Checks, domain.ProviderCheck{ID: strconv.FormatInt(pipeline.ID, 10), Name: "pipeline", Commit: out.Commit, State: state})
	}
	out.ChecksState = aggregateChecks(out.Checks, out.ChecksTruncated)
}
