package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

func (s *collection) identity(ctx context.Context) *Error {
	var result struct {
		ID json.RawMessage `json:"id"`
	}
	if _, err := s.get(ctx, s.c.prefix, &result); err != nil {
		return err
	}
	if string(result.ID) != s.c.binding.ProviderID {
		return failure("identity_mismatch")
	}
	return nil
}
func (s *collection) commitRoot(ctx context.Context) (string, *Error) {
	if s.c.binding.Provider == "github" {
		var result struct {
			SHA  string `json:"sha"`
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		}
		if _, err := s.get(ctx, s.c.prefix+"/git/commits/"+s.commit, &result); err != nil {
			return "", err
		}
		if result.SHA != s.commit || !oid(result.Tree.SHA) {
			return "", failure("object_mismatch")
		}
		return result.Tree.SHA, nil
	}
	var result struct {
		ID string `json:"id"`
	}
	if _, err := s.get(ctx, s.c.prefix+"/repository/commits/"+s.commit+"?stats=false", &result); err != nil {
		return "", err
	}
	if result.ID != s.commit {
		return "", failure("object_mismatch")
	}
	return "", nil
}

func (s *collection) tree(ctx context.Context, key string) treeResult {
	if tree, ok := s.trees[key]; ok {
		return tree
	}
	var tree treeResult
	if s.c.binding.Provider == "github" {
		tree = s.githubTree(ctx, key)
	} else {
		tree = s.gitlabTree(ctx, key)
	}
	s.trees[key] = tree
	return tree
}
func (s *collection) githubTree(ctx context.Context, id string) treeResult {
	var result struct {
		SHA       string `json:"sha"`
		Truncated *bool  `json:"truncated"`
		Tree      *[]struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			Size *int64 `json:"size"`
		} `json:"tree"`
	}
	if _, err := s.get(ctx, s.c.prefix+"/git/trees/"+id, &result); err != nil {
		return treeResult{err: err}
	}
	if result.SHA != id {
		return treeResult{err: failure("object_mismatch")}
	}
	if result.Truncated == nil || result.Tree == nil {
		return treeResult{err: failure("invalid_response")}
	}
	if *result.Truncated || len(*result.Tree) > maxDirectoryEntries {
		return treeResult{err: failure("incomplete_tree")}
	}
	entries := make(map[string]treeEntry, len(*result.Tree))
	for _, entry := range *result.Tree {
		if !validName(entry.Path) || !oid(entry.SHA) || entries[entry.Path].id != "" || entry.Size != nil && *entry.Size < 0 {
			return treeResult{err: failure("invalid_response")}
		}
		if entry.Type == "blob" && (entry.Mode == "100644" || entry.Mode == "100755") && entry.Size == nil {
			return treeResult{err: failure("invalid_response")}
		}
		entries[entry.Path] = treeEntry{name: entry.Path, id: entry.SHA, mode: entry.Mode, kind: entry.Type, size: entry.Size}
	}
	return treeResult{entries: entries}
}

func (s *collection) gitlabTree(ctx context.Context, directory string) treeResult {
	query := url.Values{"ref": {s.commit}, "recursive": {"false"}, "pagination": {"keyset"}, "per_page": {"100"}}
	if directory != "" {
		query.Set("path", directory)
	}
	endpoint := s.c.prefix + "/repository/tree"
	entries := make(map[string]treeEntry)
	seen := make(map[string]bool)
	for {
		var result *[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
		}
		headers, err := s.get(ctx, endpoint+"?"+query.Encode(), &result)
		if err != nil {
			return treeResult{err: err}
		}
		if result == nil {
			return treeResult{err: failure("invalid_response")}
		}
		if len(*result) > 100 || len(entries)+len(*result) > maxDirectoryEntries {
			return treeResult{err: failure("incomplete_tree")}
		}
		for _, entry := range *result {
			path := entry.Name
			if directory != "" {
				path = directory + "/" + entry.Name
			}
			if !validName(entry.Name) || entry.Path != path || !oid(entry.ID) || entries[entry.Name].id != "" {
				return treeResult{err: failure("invalid_response")}
			}
			entries[entry.Name] = treeEntry{name: entry.Name, id: entry.ID, mode: entry.Mode, kind: entry.Type}
		}
		next, err := s.nextPage(headers, endpoint, query)
		if err != nil {
			return treeResult{err: err}
		}
		if next == "" {
			return treeResult{entries: entries}
		}
		if len(*result) == 0 || len(entries) >= maxDirectoryEntries || seen[next] {
			return treeResult{err: failure("incomplete_tree")}
		}
		seen[next] = true
		query.Set("page_token", next)
	}
}

// Never issue requests to provider-supplied links. Extract only a bounded cursor
// after proving the link keeps the exact origin, endpoint, commit and directory.
func (s *collection) nextPage(headers http.Header, endpoint string, current url.Values) (string, *Error) {
	var next string
	for _, value := range headers.Values("Link") {
		for _, link := range strings.Split(value, ",") {
			parts := strings.Split(strings.TrimSpace(link), ";")
			if len(parts) < 2 {
				return "", failure("incomplete_tree")
			}
			isNext := false
			for _, part := range parts[1:] {
				key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
				if ok && strings.EqualFold(strings.TrimSpace(key), "rel") {
					relation := strings.TrimSpace(value)
					if isNext || (!strings.EqualFold(relation, "next") && !strings.EqualFold(relation, `"next"`)) {
						return "", failure("incomplete_tree")
					}
					isNext = true
				}
			}
			if !isNext {
				// This pinned GitLab keyset profile emits next links only. Missing
				// or unsupported relations cannot establish directory completion.
				return "", failure("incomplete_tree")
			}
			if next != "" {
				return "", failure("incomplete_tree")
			}
			address := strings.TrimSpace(parts[0])
			if len(address) < 2 || address[0] != '<' || address[len(address)-1] != '>' {
				return "", failure("incomplete_tree")
			}
			u, err := url.Parse(address[1 : len(address)-1])
			if err != nil || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.Scheme+"://"+u.Host != s.c.origin || u.EscapedPath() != endpoint {
				return "", failure("incomplete_tree")
			}
			query, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				return "", failure("incomplete_tree")
			}
			for key, values := range query {
				if len(values) != 1 || key != "page_token" && (len(current[key]) != 1 || values[0] != current.Get(key)) {
					return "", failure("incomplete_tree")
				}
			}
			for key, values := range current {
				if key != "page_token" && query.Get(key) != values[0] {
					return "", failure("incomplete_tree")
				}
			}
			next = query.Get("page_token")
			if next == "" || len(next) > 2048 || !utf8.ValidString(next) {
				return "", failure("incomplete_tree")
			}
			for _, b := range []byte(next) {
				if b < 32 || b == 127 {
					return "", failure("incomplete_tree")
				}
			}
		}
	}
	// Offset metadata alongside an absent keyset link cannot prove completion.
	if next == "" && headers.Get("X-Next-Page") != "" {
		return "", failure("incomplete_tree")
	}
	return next, nil
}
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, b := range []byte(name) {
		if b < 32 || b == 127 {
			return false
		}
	}
	return true
}

type blobResponse struct {
	SHA      string  `json:"sha"`
	Size     *int64  `json:"size"`
	Encoding string  `json:"encoding"`
	Content  *string `json:"content"`
}

func (s *collection) blob(ctx context.Context, id string) (blobResponse, *Error) {
	endpoint := s.c.prefix + "/git/blobs/" + id
	if s.c.binding.Provider == "gitlab" {
		endpoint = s.c.prefix + "/repository/blobs/" + id
	}
	var result blobResponse
	_, err := s.get(ctx, endpoint, &result)
	return result, err
}
