package delivery

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/domain"
)

func deploymentRelation(o domain.DeliveryObservation, commit string) string {
	if commit == o.Commit {
		return "published_head"
	}
	if o.State == "merged" && commit == o.MergeCommit && domain.IsLowerHex(commit, 40) {
		return "provider_merge"
	}
	return ""
}
func deploymentState(state string) string {
	switch state {
	case "success":
		return "success"
	case "failure", "error", "failed", "canceled":
		return "failed"
	case "pending", "in_progress", "queued", "created", "running", "blocked":
		return "pending"
	case "inactive":
		return "inactive"
	default:
		return "unknown"
	}
}
func validEnvironment(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
func (p *Provider) deploymentEvidence(id int64, commit, relation, environment, state string, updated time.Time) domain.ProviderDeployment {
	return domain.ProviderDeployment{ID: strconv.FormatInt(id, 10), RepositoryID: p.target.RepositoryID, ProviderProfile: p.target.Profile, Commit: commit, CommitRelation: relation, Environment: environment, ProviderState: state, State: deploymentState(state), ProviderUpdatedAt: updated.UTC(), ObservedAt: time.Now().UTC()}
}
func (p *Provider) githubDeployments(ctx context.Context, b *callBudget, out *domain.DeliveryObservation) {
	out.Deployments = []domain.ProviderDeployment{}
	out.Deployment = "not_observed"
	commits := []string{out.Commit}
	if out.State == "merged" && out.MergeCommit != out.Commit {
		commits = append(commits, out.MergeCommit)
	}
	for _, commit := range commits {
		var deployments []struct {
			ID          int64     `json:"id"`
			SHA         string    `json:"sha"`
			Environment string    `json:"environment"`
			Production  *bool     `json:"production_environment"`
			Updated     time.Time `json:"updated_at"`
		}
		headers, err := b.call(ctx, "GET", "/deployments?"+url.Values{"sha": {commit}, "per_page": {"20"}}.Encode(), nil, &deployments)
		if err != nil || len(deployments) > 20 {
			out.Deployment = "unavailable"
			return
		}
		if strings.Contains(headers.Get("Link"), `rel="next"`) {
			out.DeploymentsTruncated = true
		}
		for _, d := range deployments {
			relation := deploymentRelation(*out, d.SHA)
			if d.ID < 1 || d.SHA != commit || relation == "" || !validEnvironment(d.Environment) || d.Updated.IsZero() {
				out.Deployment = "unavailable"
				return
			}
			value := p.deploymentEvidence(d.ID, d.SHA, relation, d.Environment, "unknown", d.Updated)
			value.ProductionEnvironment = d.Production
			var statuses []struct {
				ID          int64     `json:"id"`
				State       string    `json:"state"`
				Environment string    `json:"environment"`
				Created     time.Time `json:"created_at"`
				Updated     time.Time `json:"updated_at"`
			}
			headers, err = b.call(ctx, "GET", "/deployments/"+value.ID+"/statuses?per_page=100", nil, &statuses)
			if err != nil || len(statuses) > 100 {
				out.Deployment = "unavailable"
				return
			}
			// Do not assume list order or infer a latest status from a partial history.
			if strings.Contains(headers.Get("Link"), `rel="next"`) {
				out.DeploymentsTruncated = true
				value.StatusTruncated = true
			} else {
				var latestID int64
				var latestTime time.Time
				for _, s := range statuses {
					if s.ID < 1 || s.Created.IsZero() || s.Updated.IsZero() || s.Environment != "" && !validEnvironment(s.Environment) {
						out.Deployment = "unavailable"
						return
					}
					if s.Created.After(latestTime) || s.Created.Equal(latestTime) && s.ID > latestID {
						latestID, latestTime = s.ID, s.Created
						value.StatusID = strconv.FormatInt(s.ID, 10)
						value.State, value.ProviderState = deploymentState(s.State), s.State
						value.ProviderUpdatedAt = s.Updated.UTC()
						if s.Environment != "" {
							value.Environment = s.Environment
						}
					}
				}
			}
			out.Deployments = append(out.Deployments, value)
		}
	}
	if out.DeploymentsTruncated {
		out.Deployment = "truncated"
	} else if len(out.Deployments) > 0 {
		out.Deployment = "observed"
	}
}
func (p *Provider) gitlabDeployments(ctx context.Context, b *callBudget, out *domain.DeliveryObservation) {
	out.Deployments = []domain.ProviderDeployment{}
	out.Deployment = "not_observed"
	type record struct {
		ID          int64     `json:"id"`
		SHA         string    `json:"sha"`
		Status      string    `json:"status"`
		Updated     time.Time `json:"updated_at"`
		Environment struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"environment"`
	}
	var values []record
	headers, err := b.call(ctx, "GET", "/deployments?order_by=id&sort=desc&per_page=100", nil, &values)
	if err != nil || len(values) > 100 {
		out.Deployment = "unavailable"
		return
	}
	out.DeploymentsTruncated = headers.Get("X-Next-Page") != ""
	for _, listed := range values {
		relation := deploymentRelation(*out, listed.SHA)
		if relation == "" {
			continue
		}
		if listed.ID < 1 {
			out.Deployment = "unavailable"
			return
		}
		var d record
		if _, err = b.call(ctx, "GET", "/deployments/"+strconv.FormatInt(listed.ID, 10), nil, &d); err != nil || d.ID != listed.ID || d.SHA != listed.SHA || d.Environment.ID < 1 || !validEnvironment(d.Environment.Name) || d.Updated.IsZero() {
			out.Deployment = "unavailable"
			return
		}
		value := p.deploymentEvidence(d.ID, d.SHA, relation, d.Environment.Name, d.Status, d.Updated)
		value.EnvironmentID = strconv.FormatInt(d.Environment.ID, 10)
		out.Deployments = append(out.Deployments, value)
	}
	if out.DeploymentsTruncated {
		out.Deployment = "truncated"
	} else if len(out.Deployments) > 0 {
		out.Deployment = "observed"
	}
}
