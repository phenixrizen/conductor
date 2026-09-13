package delivery

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

type githubRef struct {
	Ref    string `json:"ref"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}
type githubCommit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Tree    struct {
		SHA string `json:"sha"`
	} `json:"tree"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}
type githubPull struct {
	ID          int64      `json:"id"`
	Number      int64      `json:"number"`
	URL         string     `json:"html_url"`
	State       string     `json:"state"`
	Draft       bool       `json:"draft"`
	Body        string     `json:"body"`
	Merged      bool       `json:"merged"`
	MergedAt    *time.Time `json:"merged_at"`
	MergeCommit string     `json:"merge_commit_sha"`
	Head        struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

func githubRefPath(branch string) string { return "/git/ref/heads/" + url.PathEscape(branch) }
func (p *Provider) githubBranch(ctx context.Context, b *callBudget, branch string) (string, error) {
	var ref githubRef
	if _, err := b.call(ctx, "GET", githubRefPath(branch), nil, &ref); err != nil {
		return "", err
	}
	if ref.Ref != "refs/heads/"+branch || ref.Object.Type != "commit" || !domain.IsLowerHex(ref.Object.SHA, 40) {
		return "", ErrMismatch
	}
	return ref.Object.SHA, nil
}
func (p *Provider) githubExactCommit(ctx context.Context, b *callBudget, d domain.Delivery, sha string) error {
	var commit githubCommit
	if _, err := b.call(ctx, "GET", "/git/commits/"+sha, nil, &commit); err != nil {
		return err
	}
	if commit.SHA != sha || commit.Tree.SHA != d.ResultTree || len(commit.Parents) != 1 || commit.Parents[0].SHA != d.BaseCommit || commit.Message != commitMessage(d) {
		return ErrMismatch
	}
	return nil
}
func (p *Provider) publishGitHub(ctx context.Context, b *callBudget, d domain.Delivery, a Prepared) (domain.DeliveryObservation, error) {
	var zero domain.DeliveryObservation
	base, err := p.githubBranch(ctx, b, d.Input.BaseBranch)
	if err != nil {
		return zero, err
	}
	if base != d.BaseCommit {
		return zero, ErrMismatch
	}
	commit, err := p.githubBranch(ctx, b, d.Branch)
	if errors.Is(err, ErrMissing) {
		entries := []map[string]any{}
		for _, file := range a.Files {
			mode := file.Mode
			if mode == "000000" {
				entries = append(entries, map[string]any{"path": file.Path, "mode": file.OldMode, "type": "blob", "sha": nil})
				continue
			}
			var blob struct {
				SHA string `json:"sha"`
			}
			if _, err = b.call(ctx, "POST", "/git/blobs", map[string]any{"content": base64.StdEncoding.EncodeToString(file.Content), "encoding": "base64"}, &blob); err != nil {
				return zero, err
			}
			if blob.SHA != file.Blob {
				return zero, ErrMismatch
			}
			entries = append(entries, map[string]any{"path": file.Path, "mode": mode, "type": "blob", "sha": blob.SHA})
		}
		var tree struct {
			SHA string `json:"sha"`
		}
		if _, err = b.call(ctx, "POST", "/git/trees", map[string]any{"base_tree": d.BaseTree, "tree": entries}, &tree); err != nil {
			return zero, err
		}
		if tree.SHA != d.ResultTree {
			return zero, ErrMismatch
		}
		identity := map[string]any{"name": "Conductor", "email": "conductor@example.invalid", "date": d.CreatedAt.UTC().Format(time.RFC3339)}
		var created githubCommit
		if _, err = b.call(ctx, "POST", "/git/commits", map[string]any{"message": commitMessage(d), "tree": d.ResultTree, "parents": []string{d.BaseCommit}, "author": identity, "committer": identity}, &created); err != nil {
			return zero, err
		}
		if !domain.IsLowerHex(created.SHA, 40) {
			return zero, ErrMismatch
		}
		commit = created.SHA
		if err = p.githubExactCommit(ctx, b, d, commit); err != nil {
			return zero, err
		}
		var ref githubRef
		if _, err = b.call(ctx, "POST", "/git/refs", map[string]any{"ref": "refs/heads/" + d.Branch, "sha": commit}, &ref); err != nil {
			return zero, err
		}
		if ref.Ref != "refs/heads/"+d.Branch || ref.Object.Type != "commit" || ref.Object.SHA != commit {
			return zero, ErrMismatch
		}
	} else if err != nil {
		return zero, err
	} else if err = p.githubExactCommit(ctx, b, d, commit); err != nil {
		return zero, err
	}
	// No update-ref call exists: retries inspect the exact branch or fail. A
	// changed target baseline also blocks PR creation after a partial branch write.
	base, err = p.githubBranch(ctx, b, d.Input.BaseBranch)
	if err != nil {
		return zero, err
	}
	if base != d.BaseCommit {
		return zero, ErrMismatch
	}
	owner := strings.Split(p.target.Locator, "/")[0]
	query := url.Values{"state": {"all"}, "head": {owner + ":" + d.Branch}, "base": {d.Input.BaseBranch}, "per_page": {"100"}}
	var pulls []githubPull
	headers, err := b.call(ctx, "GET", "/pulls?"+query.Encode(), nil, &pulls)
	if err != nil {
		return zero, err
	}
	if len(pulls) > 1 || strings.Contains(headers.Get("Link"), `rel="next"`) {
		return zero, ErrMismatch
	}
	var number int64
	if len(pulls) == 1 {
		if !strings.Contains(pulls[0].Body, marker(d)) {
			return zero, ErrMismatch
		}
		number = pulls[0].Number
	} else {
		var pull githubPull
		if _, err = b.call(ctx, "POST", "/pulls", map[string]any{"title": d.Input.Title, "body": body(d), "head": d.Branch, "base": d.Input.BaseBranch, "draft": true, "maintainer_can_modify": false}, &pull); err != nil {
			return zero, err
		}
		if !pull.Draft {
			return zero, ErrMismatch
		}
		number = pull.Number
	}
	return p.observeGitHub(ctx, b, d, number, commit)
}
func (p *Provider) observeGitHub(ctx context.Context, b *callBudget, d domain.Delivery, number int64, commit string) (domain.DeliveryObservation, error) {
	out := observation(d)
	if number < 1 || !domain.IsLowerHex(commit, 40) {
		return out, ErrMismatch
	}
	var pull githubPull
	if _, err := b.call(ctx, "GET", "/pulls/"+strconv.FormatInt(number, 10), nil, &pull); err != nil {
		return out, err
	}
	if pull.Number != number || pull.ID < 1 || pull.Head.Ref != d.Branch || pull.Base.Ref != d.Input.BaseBranch || pull.Head.SHA != commit || strconv.FormatInt(pull.Head.Repo.ID, 10) != p.target.ProviderID || strconv.FormatInt(pull.Base.Repo.ID, 10) != p.target.ProviderID || !validURL(pull.URL, p.target.Host) {
		return out, ErrMismatch
	}
	if err := p.githubExactCommit(ctx, b, d, commit); err != nil {
		return out, err
	}
	out.ProviderID, out.Number, out.URL, out.Commit, out.Draft = strconv.FormatInt(pull.ID, 10), number, pull.URL, commit, pull.Draft
	switch {
	case pull.Merged && pull.MergedAt != nil && domain.IsLowerHex(pull.MergeCommit, 40):
		out.State = "merged"
		out.MergeCommit = pull.MergeCommit
	case pull.State == "closed" && !pull.Merged && pull.MergedAt == nil:
		out.State = "closed"
	case pull.State == "open" && pull.MergedAt == nil:
		out.State = "open"
		if pull.Draft {
			out.State = "draft"
		}
	default:
		return out, ErrMismatch
	}
	p.githubChecks(ctx, b, &out)
	p.githubDeployments(ctx, b, &out)
	return out, nil
}
func (p *Provider) githubChecks(ctx context.Context, b *callBudget, out *domain.DeliveryObservation) {
	var checks struct {
		Total int `json:"total_count"`
		Runs  []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Head       string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	headers, err := b.call(ctx, "GET", "/commits/"+out.Commit+"/check-runs?per_page=100", nil, &checks)
	if err != nil {
		out.ChecksState = "unavailable"
		return
	}
	if checks.Total > len(checks.Runs) || len(checks.Runs) > 100 || strings.Contains(headers.Get("Link"), `rel="next"`) {
		out.ChecksTruncated = true
	}
	for _, check := range checks.Runs[:min(len(checks.Runs), 100)] {
		state := "unknown"
		if check.Head != out.Commit || check.ID < 1 || len(check.Name) > 256 {
			out.ChecksState = "unavailable"
			return
		}
		if check.Status == "queued" || check.Status == "in_progress" {
			state = "pending"
		} else if check.Status == "completed" {
			switch check.Conclusion {
			case "success":
				state = "passed"
			case "failure", "cancelled", "timed_out", "action_required", "startup_failure":
				state = "failed"
			case "neutral", "skipped":
				state = "unexecuted"
			}
		}
		out.Checks = append(out.Checks, domain.ProviderCheck{ID: strconv.FormatInt(check.ID, 10), Name: check.Name, Commit: out.Commit, State: state})
	}
	var status struct {
		SHA      string `json:"sha"`
		Total    int    `json:"total_count"`
		Statuses []struct {
			ID      int64  `json:"id"`
			Context string `json:"context"`
			State   string `json:"state"`
		} `json:"statuses"`
	}
	headers, err = b.call(ctx, "GET", "/commits/"+out.Commit+"/status?per_page=100", nil, &status)
	if err != nil || status.SHA != out.Commit {
		out.ChecksState = "unavailable"
		return
	}
	if status.Total > len(status.Statuses) || len(status.Statuses) > 100 || strings.Contains(headers.Get("Link"), `rel="next"`) {
		out.ChecksTruncated = true
	}
	for _, check := range status.Statuses[:min(len(status.Statuses), 100)] {
		state := "unknown"
		switch check.State {
		case "success":
			state = "passed"
		case "pending":
			state = "pending"
		case "failure", "error":
			state = "failed"
		}
		if check.ID < 1 || len(check.Context) > 256 {
			out.ChecksState = "unavailable"
			return
		}
		out.Checks = append(out.Checks, domain.ProviderCheck{ID: "status/" + strconv.FormatInt(check.ID, 10), Name: check.Context, Commit: out.Commit, State: state})
	}
	out.ChecksState = aggregateChecks(out.Checks, out.ChecksTruncated)
}
func aggregateChecks(checks []domain.ProviderCheck, truncated bool) string {
	if len(checks) == 0 || truncated {
		return "unknown"
	}
	state := "passed"
	for _, check := range checks {
		if check.State == "failed" {
			return "failed"
		}
		if check.State != "passed" {
			state = "unknown"
			if check.State == "pending" {
				state = "pending"
			}
		}
	}
	return state
}
