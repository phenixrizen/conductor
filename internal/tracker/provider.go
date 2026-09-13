// Package tracker implements the pinned Linear GraphQL and Jira Cloud REST v3
// profiles. Planning fields are read-only; writes target one Conductor link card.
package tracker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

var ErrUnavailable = errors.New("tracker unavailable")
var ErrIdentity = errors.New("tracker identity mismatch")
var ErrUncertain = errors.New("tracker write outcome unknown")

type Credential struct {
	Token string `json:"token"`
	Email string `json:"email,omitempty"`
	OAuth bool   `json:"oauth,omitempty"`
}
type Adapter struct {
	config     domain.TrackerConfig
	credential Credential
	client     *http.Client
	origin     string
	authorize  func(context.Context) error
}
type Options struct {
	HTTPClient            *http.Client
	Origin                string
	AllowInsecureLoopback bool
}

func New(config domain.TrackerConfig, credential Credential, authorize func(context.Context) error, options Options) (*Adapter, error) {
	if domain.ValidateTrackerConfig(config) != nil || authorize == nil || len(credential.Token) < 1 || len(credential.Token) > 8192 || strings.ContainsAny(credential.Token, "\r\n\x00 \t") || !domain.TrackerText(credential.Email, 256) || config.Provider == "jira" && (credential.Email == "" || credential.OAuth) {
		return nil, domain.ErrInvalidInput
	}
	origin := "https://" + config.Host
	if config.Provider == "linear" {
		origin = "https://api.linear.app"
	}
	if options.Origin != "" {
		u, e := url.Parse(options.Origin)
		if e != nil || u.Scheme != "http" || !options.AllowInsecureLoopback || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			return nil, domain.ErrInvalidInput
		}
		origin = options.Origin
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DisableKeepAlives = true
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	tr.ResponseHeaderTimeout = 10 * time.Second
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	if options.HTTPClient != nil {
		copy := *options.HTTPClient
		client = &copy
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{config: config, credential: credential, client: client, origin: origin, authorize: authorize}, nil
}
func (a *Adapter) call(ctx context.Context, method, path string, input, output any, missingOK bool) error {
	if e := a.authorize(ctx); e != nil {
		return e
	}
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil || len(b) > 64<<10 {
			return domain.ErrInvalidInput
		}
		body = bytes.NewReader(b)
	}
	request, e := http.NewRequestWithContext(ctx, method, a.origin+path, body)
	if e != nil {
		return ErrUnavailable
	}
	request.GetBody = nil // A hidden replay can conceal an uncertain external write.
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if a.config.Provider == "linear" {
		token := a.credential.Token
		if a.credential.OAuth {
			token = "Bearer " + token
		}
		request.Header.Set("Authorization", token)
	} else {
		request.SetBasicAuth(a.credential.Email, a.credential.Token)
	}
	response, e := a.client.Do(request)
	if e != nil {
		if method == "POST" {
			return ErrUncertain
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	if missingOK && response.StatusCode == 404 {
		return errMissing
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if method == "POST" {
			return ErrUncertain
		}
		return ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if e != nil || len(b) > 512<<10 {
		if method == "POST" {
			return ErrUncertain
		}
		return ErrUnavailable
	}
	if output != nil && (len(b) == 0 || json.Unmarshal(b, output) != nil) {
		return ErrUnavailable
	}
	return nil
}

var errMissing = errors.New("tracker record missing")

func (a *Adapter) graphql(ctx context.Context, query string, variables any, out any) error {
	var result struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	e := a.call(ctx, "POST", "/graphql", map[string]any{"query": query, "variables": variables}, &result, false)
	if e != nil {
		return e
	}
	if len(result.Errors) > 0 || len(result.Data) == 0 || string(result.Data) == "null" || json.Unmarshal(result.Data, out) != nil {
		return ErrUnavailable
	}
	return nil
}

const linearRead = `query ConductorTrackerIssue($id:String!,$url:String!){organization{id} issue(id:$id){id identifier url title description priority updatedAt team{id} assignee{id} state{id name}} attachmentsForURL(url:$url,first:50,includeArchived:true){nodes{url title subtitle issue{id}} pageInfo{hasNextPage}}}`

func (a *Adapter) Read(ctx context.Context, issueID, projectionURL string) (domain.TrackerIssue, *domain.TrackerProjection, error) {
	var issue domain.TrackerIssue
	var projection *domain.TrackerProjection
	if !domain.TrackerRecordID(a.config.Provider, issueID) {
		return issue, nil, domain.ErrInvalidInput
	}
	if a.config.Provider == "linear" {
		var data struct {
			Organization struct{ ID string }
			Issue        struct {
				ID, Identifier, URL, Title string
				Description                json.RawMessage
				Priority                   json.Number
				UpdatedAt                  time.Time
				Team                       struct{ ID string }
				Assignee                   *struct{ ID string }
				State                      struct{ ID, Name string }
			}
			AttachmentsForURL struct {
				Nodes []struct {
					URL, Title, Subtitle string
					Issue                struct{ ID string }
				}
				PageInfo struct{ HasNextPage bool }
			}
		}
		if e := a.graphql(ctx, linearRead, map[string]string{"id": issueID, "url": projectionURL}, &data); e != nil {
			return issue, nil, e
		}
		if data.Organization.ID != a.config.OrganizationID || data.Issue.ID != issueID || data.Issue.Team.ID != a.config.ScopeID || data.AttachmentsForURL.PageInfo.HasNextPage || len(data.AttachmentsForURL.Nodes) > 50 {
			return issue, nil, ErrIdentity
		}
		v := data.Issue
		issue = domain.TrackerIssue{ID: v.ID, Key: v.Identifier, URL: v.URL, Title: v.Title, Description: v.Description, Priority: string(v.Priority), StatusID: v.State.ID, StatusName: v.State.Name, UpdatedAt: v.UpdatedAt}
		if v.Assignee != nil {
			issue.AssigneeID = v.Assignee.ID
		}
		for _, n := range data.AttachmentsForURL.Nodes {
			if n.Issue.ID == issueID && n.URL == projectionURL {
				if projection != nil {
					return issue, nil, ErrUnavailable
				}
				projection = &domain.TrackerProjection{URL: n.URL, Title: n.Title, Summary: n.Subtitle}
			}
		}
	} else {
		var data struct {
			ID, Key string
			Fields  struct {
				Summary     string
				Description json.RawMessage
				Updated     string
				Project     struct{ ID string }
				Priority    *struct{ ID, Name string }
				Assignee    *struct{ AccountID string }
				Status      struct{ ID, Name string }
			}
		}
		if e := a.call(ctx, "GET", "/rest/api/3/issue/"+url.PathEscape(issueID)+"?fields=summary,description,updated,project,priority,assignee,status", nil, &data, false); e != nil {
			return issue, nil, e
		}
		if data.ID != issueID || data.Fields.Project.ID != a.config.ScopeID {
			return issue, nil, ErrIdentity
		}
		v := data.Fields
		updated, e := time.Parse("2006-01-02T15:04:05.000-0700", v.Updated)
		if e != nil {
			updated, e = time.Parse(time.RFC3339Nano, v.Updated)
		}
		if e != nil {
			return issue, nil, ErrUnavailable
		}
		issue = domain.TrackerIssue{ID: data.ID, Key: data.Key, URL: "https://" + a.config.Host + "/browse/" + url.PathEscape(data.Key), Title: v.Summary, Description: v.Description, StatusID: v.Status.ID, StatusName: v.Status.Name, UpdatedAt: updated}
		if v.Priority != nil {
			issue.Priority = v.Priority.Name
		}
		if v.Assignee != nil {
			issue.AssigneeID = v.Assignee.AccountID
		}
		var link struct {
			GlobalID string                   `json:"globalId"`
			Object   domain.TrackerProjection `json:"object"`
		}
		e = a.call(ctx, "GET", "/rest/api/3/issue/"+url.PathEscape(issueID)+"/remotelink?globalId="+url.QueryEscape(projectionURL), nil, &link, true)
		if e != nil && !errors.Is(e, errMissing) {
			return issue, nil, e
		}
		if e == nil {
			if link.GlobalID != projectionURL {
				return issue, nil, ErrIdentity
			}
			projection = &link.Object
		}
	}
	if issue.ID == "" || issue.Key == "" || issue.Title == "" || issue.UpdatedAt.IsZero() || !domain.TrackerRecordID(a.config.Provider, issue.StatusID) || !domain.TrackerText(issue.Key, 128) || !domain.TrackerText(issue.Title, 1024) || !domain.TrackerText(issue.StatusName, 256) || !domain.TrackerText(issue.Priority, 256) || !domain.TrackerText(issue.AssigneeID, 256) || len(issue.Description) > 64<<10 {
		return domain.TrackerIssue{}, nil, ErrUnavailable
	}
	u, e := url.Parse(issue.URL)
	if e != nil || u.Scheme != "https" || u.Host != a.config.Host || u.User != nil || len(issue.URL) > 2048 {
		return domain.TrackerIssue{}, nil, ErrIdentity
	}
	if projection != nil && (!domain.TrackerText(projection.Title, 1024) || !domain.TrackerText(projection.Summary, 32<<10) || len(projection.URL) > 2048) {
		return domain.TrackerIssue{}, nil, ErrUnavailable
	}
	if len(issue.Description) == 0 {
		issue.Description = json.RawMessage("null")
	}
	domain.MapTrackerStatus(a.config, &issue)
	return issue, projection, nil
}
func (a *Adapter) Write(ctx context.Context, issueID string, projection domain.TrackerProjection) error {
	if !domain.TrackerRecordID(a.config.Provider, issueID) {
		return domain.ErrInvalidInput
	}
	if a.config.Provider == "linear" {
		var out struct {
			AttachmentCreate struct {
				Success    bool
				Attachment struct{ ID string }
			}
		}
		e := a.graphql(ctx, `mutation ConductorTrackerProjection($input:AttachmentCreateInput!){attachmentCreate(input:$input){success attachment{id}}}`, map[string]any{"input": map[string]string{"issueId": issueID, "url": projection.URL, "title": projection.Title, "subtitle": projection.Summary}}, &out)
		if e != nil || !out.AttachmentCreate.Success || out.AttachmentCreate.Attachment.ID == "" {
			return ErrUncertain
		}
		return nil
	}
	var out struct{ ID json.Number }
	e := a.call(ctx, "POST", "/rest/api/3/issue/"+url.PathEscape(issueID)+"/remotelink", map[string]any{"globalId": projection.URL, "application": map[string]string{"type": "conductor", "name": "Conductor"}, "relationship": "tracked by", "object": projection}, &out, false)
	if e != nil {
		return ErrUncertain
	}
	if _, e = strconv.ParseInt(string(out.ID), 10, 64); e != nil {
		return ErrUncertain
	}
	return nil
}
func ProjectionDigest(p *domain.TrackerProjection) string {
	if p == nil {
		return ""
	}
	return domain.TrackerDigest(p)
}
