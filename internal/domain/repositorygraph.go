package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	RepositoryGraphIndexer = "conductor-native/v1"
	MaxGraphSources        = 16
	MaxGraphNodes          = 4096
	MaxGraphEdges          = 8192
	MaxGraphGaps           = 512
	MaxGraphQueryNodes     = 100
)

// GraphSource selects an already inspected immutable receipt. Repository labels
// and client-supplied source text cannot establish graph provenance.
type GraphSource struct {
	RepositoryID     string `json:"repositoryId"`
	CollectionID     string `json:"collectionId"`
	Digest           string `json:"digest"`
	FullSourceDigest string `json:"fullSourceDigest,omitempty"`
}
type GraphInput struct {
	Sources []GraphSource `json:"sources"`
}
type GraphSourceRecord struct {
	GraphSource
	Commit      string    `json:"commit"`
	CollectedAt time.Time `json:"collectedAt"`
	Freshness   string    `json:"freshness"`
}
type GraphNode struct {
	ID             string `json:"id"`
	RepositoryID   string `json:"repositoryId"`
	CollectionID   string `json:"collectionId"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Path           string `json:"path,omitempty"`
	ArtifactDigest string `json:"artifactDigest,omitempty"`
	Line           int    `json:"line,omitempty"`
}
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	// Evidence identifies the declaration/import, not proof of runtime behavior.
	Evidence   string `json:"evidence"`
	Provenance string `json:"provenance,omitempty"`
}
type GraphGap struct {
	RepositoryID string `json:"repositoryId"`
	Path         string `json:"path,omitempty"`
	State        string `json:"state"`
	Message      string `json:"message"`
}
type GraphSnapshot struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Indexer       string              `json:"indexer"`
	Sources       []GraphSourceRecord `json:"sources"`
	Nodes         []GraphNode         `json:"nodes"`
	Edges         []GraphEdge         `json:"edges"`
	Gaps          []GraphGap          `json:"gaps"`
	Truncated     bool                `json:"truncated"`
}
type RepositoryGraph struct {
	ID          string        `json:"id"`
	WorkspaceID string        `json:"workspaceId"`
	CreatorID   string        `json:"creatorId"`
	Digest      string        `json:"digest"`
	CreatedAt   time.Time     `json:"createdAt"`
	Snapshot    GraphSnapshot `json:"snapshot"`
}
type RepositoryGraphSummary struct {
	ID        string    `json:"id"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"createdAt"`
}
type RepositoryGraphPage struct {
	Graphs     []RepositoryGraphSummary `json:"graphs"`
	NextBefore string                   `json:"nextBefore,omitempty"`
}
type GraphQuery struct {
	Search string `json:"search,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	Depth  int    `json:"depth"`
	Limit  int    `json:"limit"`
}
type GraphQueryResult struct {
	GraphID   string              `json:"graphId"`
	Digest    string              `json:"digest"`
	Indexer   string              `json:"indexer"`
	Nodes     []GraphNode         `json:"nodes"`
	Edges     []GraphEdge         `json:"edges"`
	Gaps      []GraphGap          `json:"gaps"`
	Sources   []GraphSourceRecord `json:"sources"`
	Truncated bool                `json:"truncated"`
}

func NormalizeGraphInput(input GraphInput) (GraphInput, error) {
	if len(input.Sources) < 1 || len(input.Sources) > MaxGraphSources {
		return GraphInput{}, ErrInvalidInput
	}
	input.Sources = append([]GraphSource(nil), input.Sources...)
	sort.Slice(input.Sources, func(i, j int) bool { return input.Sources[i].RepositoryID < input.Sources[j].RepositoryID })
	for i, s := range input.Sources {
		if ValidateAccessID(s.RepositoryID) != nil || !IsLowerHex(s.CollectionID, 32) || !IsLowerHex(s.Digest, 64) || s.FullSourceDigest != "" && !IsLowerHex(s.FullSourceDigest, 64) || i > 0 && input.Sources[i-1].RepositoryID == s.RepositoryID {
			return GraphInput{}, ErrInvalidInput
		}
	}
	return input, nil
}
func ValidateGraphQuery(q GraphQuery) error {
	if q.Depth < 0 || q.Depth > 5 || q.Limit < 1 || q.Limit > MaxGraphQueryNodes || len(q.Search) > 256 || q.Search != "" && !validContextLabel(q.Search, 256) || q.NodeID != "" && !IsLowerHex(q.NodeID, 64) || q.NodeID != "" && q.Search != "" {
		return ErrInvalidInput
	}
	return nil
}

// QueryGraph is a deterministic, bounded projection of one authorized snapshot.
// It follows both directions for impact investigation; every returned edge has
// both endpoints present. Truncation never implies the absence of more matches.
func QueryGraph(g RepositoryGraph, q GraphQuery) (GraphQueryResult, error) {
	out := GraphQueryResult{GraphID: g.ID, Digest: g.Digest, Indexer: g.Snapshot.Indexer, Nodes: []GraphNode{}, Edges: []GraphEdge{}, Gaps: g.Snapshot.Gaps, Sources: g.Snapshot.Sources, Truncated: g.Snapshot.Truncated}
	if err := ValidateGraphQuery(q); err != nil {
		return out, err
	}
	selected := map[string]bool{}
	add := func(id string) {
		if selected[id] {
			return
		}
		if len(selected) >= q.Limit {
			out.Truncated = true
			return
		}
		selected[id] = true
	}
	for _, node := range g.Snapshot.Nodes {
		if q.NodeID != "" {
			if node.ID == q.NodeID {
				add(node.ID)
			}
		} else if q.Search == "" || strings.Contains(strings.ToLower(node.Name), strings.ToLower(q.Search)) || strings.Contains(strings.ToLower(node.Path), strings.ToLower(q.Search)) {
			add(node.ID)
		}
	}
	if q.NodeID != "" && len(selected) == 0 {
		return out, ErrNotFound
	}
	for depth := 0; depth < q.Depth; depth++ {
		frontier := map[string]bool{}
		for id := range selected {
			frontier[id] = true
		}
		for _, edge := range g.Snapshot.Edges {
			if frontier[edge.From] {
				add(edge.To)
			}
			if frontier[edge.To] {
				add(edge.From)
			}
		}
	}
	for _, node := range g.Snapshot.Nodes {
		if selected[node.ID] {
			out.Nodes = append(out.Nodes, node)
		}
	}
	for _, edge := range g.Snapshot.Edges {
		if selected[edge.From] && selected[edge.To] {
			out.Edges = append(out.Edges, edge)
		}
	}
	return out, nil
}

const CodeGraphIndexer = "colbymchenry/codegraph/1.6.0"

// CodeGraphIndex is trusted activity output, never accepted from a public client.
// Nodes are structurally derived source evidence; even a resolved call is not an
// executed check. NativeFiles reports actual native extraction probes, while
// other files may require CodeGraph's portable fallback or remain unsupported.
type CodeGraphIndex struct {
	Indexer       string          `json:"indexer"`
	KernelVersion string          `json:"kernelVersion"`
	NativeFiles   int             `json:"nativeFiles"`
	Files         []CodeGraphFile `json:"files"`
	Nodes         []CodeGraphNode `json:"nodes"`
	Edges         []CodeGraphEdge `json:"edges"`
	Unresolved    int             `json:"unresolved"`
	Truncated     bool            `json:"truncated"`
}
type CodeGraphFile struct {
	Path  string `json:"path"`
	State string `json:"state"`
}
type CodeGraphNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path"`
	Line int    `json:"line"`
}
type CodeGraphEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`
	Provenance string `json:"provenance"`
}

func ValidateCodeGraphIndex(index CodeGraphIndex, artifacts []ContextArtifact) error {
	if index.Indexer != CodeGraphIndexer || !validContextLabel(index.KernelVersion, 128) || index.NativeFiles < 0 || index.NativeFiles > len(artifacts) || len(index.Files) > len(artifacts) || len(index.Nodes) > MaxGraphNodes || len(index.Edges) > MaxGraphEdges || index.Unresolved < 0 {
		return ErrInvalidInput
	}
	files := map[string]int{}
	for _, a := range artifacts {
		if a.State == "collected" && a.Text != nil {
			files[a.Path] = strings.Count(*a.Text, "\n") + 1
		}
	}
	seenFiles := map[string]bool{}
	native := 0
	for _, f := range index.Files {
		if files[f.Path] == 0 || seenFiles[f.Path] || f.State != "native" && f.State != "unsupported_or_deferred" {
			return ErrInvalidInput
		}
		seenFiles[f.Path] = true
		if f.State == "native" {
			native++
		}
	}
	if native != index.NativeFiles || len(seenFiles) != len(files) {
		return ErrInvalidInput
	}
	ids := map[string]bool{}
	for _, n := range index.Nodes {
		if !validContextLabel(n.ID, 1024) || ids[n.ID] || !validContextLabel(n.Kind, 64) || !validContextLabel(n.Name, 1024) || files[n.Path] == 0 || n.Line < 0 || n.Line > files[n.Path] {
			return ErrInvalidInput
		}
		ids[n.ID] = true
	}
	for _, e := range index.Edges {
		if !ids[e.From] || !ids[e.To] || !validContextLabel(e.Kind, 64) || !validContextLabel(e.Provenance, 64) {
			return ErrInvalidInput
		}
	}
	return nil
}

// Receipt selectors reject duplicate and case-aliased keys, so all interfaces
// confirm one unambiguous source identity even inside an array of repositories.
func (s *GraphSource) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return ErrInvalidInput
		}
		name, ok := key.(string)
		if !ok || seen[name] || name != "repositoryId" && name != "collectionId" && name != "digest" && name != "fullSourceDigest" {
			return ErrInvalidInput
		}
		seen[name] = true
		var discard json.RawMessage
		if d.Decode(&discard) != nil {
			return ErrInvalidInput
		}
	}
	if _, err = d.Token(); err != nil {
		return ErrInvalidInput
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return ErrInvalidInput
	}
	type plain GraphSource
	var value plain
	if json.Unmarshal(data, &value) != nil {
		return ErrInvalidInput
	}
	*s = GraphSource(value)
	return nil
}

// An embedded selector's strict decoder must not consume the record's additional
// immutable provenance fields through encoding/json's promoted-method rules.
func (s *GraphSourceRecord) UnmarshalJSON(data []byte) error {
	var value struct {
		RepositoryID     string    `json:"repositoryId"`
		CollectionID     string    `json:"collectionId"`
		Digest           string    `json:"digest"`
		Commit           string    `json:"commit"`
		CollectedAt      time.Time `json:"collectedAt"`
		Freshness        string    `json:"freshness"`
		FullSourceDigest string    `json:"fullSourceDigest,omitempty"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = GraphSourceRecord{GraphSource: GraphSource{RepositoryID: value.RepositoryID, CollectionID: value.CollectionID, Digest: value.Digest, FullSourceDigest: value.FullSourceDigest}, Commit: value.Commit, CollectedAt: value.CollectedAt, Freshness: value.Freshness}
	return nil
}
