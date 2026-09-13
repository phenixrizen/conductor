// Package repositorygraph derives bounded structural evidence without executing
// repository code, resolving a network dependency, or assigning review authority.
package repositorygraph

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/phenixrizen/conductor/internal/domain"
	"golang.org/x/mod/modfile"
)

type Receipt struct {
	Source  domain.GraphSource
	Receipt domain.CollectionReceipt
	Index   *domain.CodeGraphIndex
}
type dependency struct{ node, repository, name, ecosystem string }
type module struct{ node, repository, name, ecosystem string }
type builder struct {
	graph        domain.GraphSnapshot
	nodes        map[string]bool
	edges        map[string]bool
	modules      []module
	dependencies []dependency
}

func Build(ctx context.Context, receipts []Receipt) (domain.GraphSnapshot, error) {
	b := builder{graph: domain.GraphSnapshot{SchemaVersion: 1, Indexer: domain.RepositoryGraphIndexer, Sources: []domain.GraphSourceRecord{}, Nodes: []domain.GraphNode{}, Edges: []domain.GraphEdge{}, Gaps: []domain.GraphGap{}}, nodes: map[string]bool{}, edges: map[string]bool{}}
	for _, r := range receipts {
		if err := ctx.Err(); err != nil {
			return domain.GraphSnapshot{}, err
		}
		s := r.Receipt.Snapshot
		if s.Source == nil || s.Source.RepositoryID != r.Source.RepositoryID || s.CollectionID != r.Source.CollectionID || r.Receipt.Digest != r.Source.Digest || domain.ValidateRepositoryContext(s) != nil {
			return domain.GraphSnapshot{}, domain.ErrInvalidInput
		}
		digest, err := domain.JSONDigest(s)
		if err != nil || digest != r.Source.Digest {
			return domain.GraphSnapshot{}, domain.ErrInvalidInput
		}
		b.graph.Sources = append(b.graph.Sources, domain.GraphSourceRecord{GraphSource: r.Source, Commit: s.Commit, CollectedAt: s.CollectedAt, Freshness: "unknown"})
		root := b.node(r.Source, "repository", r.Source.RepositoryID, "", "", 0)
		b.gap(r.Source.RepositoryID, "", "partial", "Only explicitly collected paths were indexed; branch freshness and uncollected files are unknown.")
		for _, a := range s.Artifacts {
			if err := ctx.Err(); err != nil {
				return domain.GraphSnapshot{}, err
			}
			file := b.node(r.Source, "file", a.Path, a.Path, a.Digest, 0)
			b.edge(root, file, "contains", file)
			if a.State != "collected" || a.Text == nil {
				b.gap(r.Source.RepositoryID, a.Path, a.State, "Receipt did not contain complete source text.")
				continue
			}
			switch {
			case path.Base(a.Path) == "go.mod":
				b.goModule(r.Source, a, file)
			case path.Base(a.Path) == "package.json":
				b.npmModule(r.Source, a, file)
			case strings.HasSuffix(a.Path, ".go"):
				b.goFile(r.Source, a, file)
			default:
				b.gap(r.Source.RepositoryID, a.Path, "unsupported", "Native indexer supports Go declarations/imports, go.mod and package.json dependency declarations.")
			}
		}
		if r.Index != nil {
			if err := domain.ValidateCodeGraphIndex(*r.Index, s.Artifacts); err != nil {
				return domain.GraphSnapshot{}, err
			}
			b.codeGraph(r)
		} else {
			b.gap(r.Source.RepositoryID, "", "unavailable", "No CodeGraph index is recorded for this receipt; only native Go/manifest analysis is available.")
		}
	}
	// Resolve only against explicit manifests in this snapshot. Duplicate module
	// declarations remain ambiguous; choosing one would invent a repository edge.
	for _, d := range b.dependencies {
		var matches []module
		longest := 0
		for _, m := range b.modules {
			if m.ecosystem == d.ecosystem && (d.name == m.name || d.ecosystem == "go" && strings.HasPrefix(d.name, m.name+"/")) {
				if len(m.name) > longest {
					matches = nil
					longest = len(m.name)
				}
				if len(m.name) == longest {
					matches = append(matches, m)
				}
			}
		}
		if len(matches) == 1 {
			b.edge(d.node, matches[0].node, "resolves_to", d.node)
			if d.repository != matches[0].repository {
				b.edge(b.repositoryNode(d.repository), b.repositoryNode(matches[0].repository), "depends_on", d.node)
			}
		} else {
			b.gap(d.repository, "", "unresolved", "A dependency has no unique matching manifest in the selected receipts.")
		}
	}
	sort.Slice(b.graph.Nodes, func(i, j int) bool { return b.graph.Nodes[i].ID < b.graph.Nodes[j].ID })
	sort.Slice(b.graph.Edges, func(i, j int) bool {
		a, c := b.graph.Edges[i], b.graph.Edges[j]
		return a.From+a.To+a.Kind+a.Evidence < c.From+c.To+c.Kind+c.Evidence
	})
	// Keep full inspection within the shared client's 2 MiB response bound.
	// Source metadata and gaps survive; omitted nodes/edges are explicit truncation.
	for {
		data, err := json.Marshal(b.graph)
		if err != nil {
			return domain.GraphSnapshot{}, err
		}
		if len(data) <= 1<<20 {
			break
		}
		b.graph.Truncated = true
		if len(b.graph.Nodes) == 0 {
			return domain.GraphSnapshot{}, domain.ErrCapacity
		}
		b.graph.Nodes = b.graph.Nodes[:len(b.graph.Nodes)/2]
		kept := map[string]bool{}
		for _, n := range b.graph.Nodes {
			kept[n.ID] = true
		}
		edges := []domain.GraphEdge{}
		for _, e := range b.graph.Edges {
			if kept[e.From] && kept[e.To] && kept[e.Evidence] {
				edges = append(edges, e)
			}
		}
		b.graph.Edges = edges
	}
	return b.graph, nil
}
func (b *builder) node(s domain.GraphSource, kind, name, p, digest string, line int) string {
	if len(name) > 1024 {
		b.gap(s.RepositoryID, p, "truncated", "Symbol or dependency name exceeds the index limit.")
		return ""
	}
	id, _ := domain.JSONDigest([]any{s.RepositoryID, s.CollectionID, kind, name, p, line})
	if b.nodes[id] {
		return id
	}
	if len(b.graph.Nodes) >= domain.MaxGraphNodes {
		b.graph.Truncated = true
		return ""
	}
	b.nodes[id] = true
	b.graph.Nodes = append(b.graph.Nodes, domain.GraphNode{ID: id, RepositoryID: s.RepositoryID, CollectionID: s.CollectionID, Kind: kind, Name: name, Path: p, ArtifactDigest: digest, Line: line})
	return id
}
func (b *builder) edge(from, to, kind, evidence string) {
	if from == "" || to == "" || evidence == "" {
		return
	}
	key := from + to + kind + evidence
	if b.edges[key] {
		return
	}
	if len(b.graph.Edges) >= domain.MaxGraphEdges {
		b.graph.Truncated = true
		return
	}
	b.edges[key] = true
	b.graph.Edges = append(b.graph.Edges, domain.GraphEdge{From: from, To: to, Kind: kind, Evidence: evidence})
}
func (b *builder) gap(repo, p, state, message string) {
	if len(b.graph.Gaps) >= domain.MaxGraphGaps {
		b.graph.Truncated = true
		return
	}
	b.graph.Gaps = append(b.graph.Gaps, domain.GraphGap{RepositoryID: repo, Path: p, State: state, Message: message})
}
func (b *builder) repositoryNode(repo string) string {
	for _, n := range b.graph.Nodes {
		if n.RepositoryID == repo && n.Kind == "repository" {
			return n.ID
		}
	}
	return ""
}
func (b *builder) dep(s domain.GraphSource, a domain.ContextArtifact, file, name, ecosystem string, line int) {
	n := b.node(s, "dependency", name, a.Path, a.Digest, line)
	b.edge(file, n, "imports", n)
	b.dependencies = append(b.dependencies, dependency{n, s.RepositoryID, name, ecosystem})
}
func (b *builder) goModule(s domain.GraphSource, a domain.ContextArtifact, file string) {
	f, err := modfile.Parse(a.Path, []byte(*a.Text), nil)
	if err != nil || f.Module == nil {
		b.gap(s.RepositoryID, a.Path, "unavailable", "Go module manifest could not be parsed.")
		return
	}
	n := b.node(s, "module", f.Module.Mod.Path, a.Path, a.Digest, f.Module.Syntax.Start.Line)
	b.edge(file, n, "declares", n)
	b.modules = append(b.modules, module{n, s.RepositoryID, f.Module.Mod.Path, "go"})
	for _, r := range f.Require {
		b.dep(s, a, file, r.Mod.Path, "go", r.Syntax.Start.Line)
	}
	if len(f.Replace) > 0 || len(f.Exclude) > 0 {
		b.gap(s.RepositoryID, a.Path, "unsupported", "Go replace and exclude directives are recorded as source only; declared dependency edges do not resolve their overrides.")
	}
}
func (b *builder) npmModule(s domain.GraphSource, a domain.ContextArtifact, file string) {
	var m struct {
		Name                 string                     `json:"name"`
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	}
	if json.Unmarshal([]byte(*a.Text), &m) != nil {
		b.gap(s.RepositoryID, a.Path, "unavailable", "Package manifest could not be parsed.")
		return
	}
	if m.Name != "" {
		n := b.node(s, "module", m.Name, a.Path, a.Digest, 0)
		b.edge(file, n, "declares", n)
		b.modules = append(b.modules, module{n, s.RepositoryID, m.Name, "npm"})
	}
	names := map[string]bool{}
	for _, group := range []map[string]json.RawMessage{m.Dependencies, m.DevDependencies, m.PeerDependencies, m.OptionalDependencies} {
		for name := range group {
			names[name] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		b.dep(s, a, file, name, "npm", 0)
	}
	b.gap(s.RepositoryID, a.Path, "partial", "Package names establish declared relationships only; versions, aliases, workspaces and runtime resolution are not verified.")
}
func (b *builder) goFile(s domain.GraphSource, a domain.ContextArtifact, file string) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, a.Path, *a.Text, parser.SkipObjectResolution)
	if err != nil {
		b.gap(s.RepositoryID, a.Path, "unavailable", "Go source could not be parsed; partial declarations were not indexed.")
		return
	}
	for _, i := range f.Imports {
		if name, err := strconv.Unquote(i.Path.Value); err == nil {
			b.dep(s, a, file, name, "go", fs.Position(i.Pos()).Line)
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = receiver(d.Recv.List[0].Type) + "." + name
			}
			n := b.node(s, "function", name, a.Path, a.Digest, fs.Position(d.Pos()).Line)
			b.edge(file, n, "declares", n)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					n := b.node(s, "type", spec.Name.Name, a.Path, a.Digest, fs.Position(spec.Pos()).Line)
					b.edge(file, n, "declares", n)
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						n := b.node(s, "value", name.Name, a.Path, a.Digest, fs.Position(name.Pos()).Line)
						b.edge(file, n, "declares", n)
					}
				}
			}
		}
	}
	b.gap(s.RepositoryID, a.Path, "partial", "Go syntax is indexed without type checking, build-tag selection, call resolution or executed verification.")
}
func receiver(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiver(e.X)
	case *ast.IndexExpr:
		return receiver(e.X)
	case *ast.IndexListExpr:
		return receiver(e.X)
	default:
		return "receiver"
	}
}

func (b *builder) codeGraph(r Receipt) {
	index := r.Index
	b.graph.Indexer = domain.RepositoryGraphIndexer + "+" + domain.CodeGraphIndexer
	if index.Truncated {
		b.graph.Truncated = true
	}
	digests := map[string]string{}
	for _, a := range r.Receipt.Snapshot.Artifacts {
		digests[a.Path] = a.Digest
	}
	ids := map[string]string{}
	for _, n := range index.Nodes {
		ids[n.ID] = b.node(r.Source, "codegraph:"+n.Kind, n.Name, n.Path, digests[n.Path], n.Line)
	}
	for _, e := range index.Edges {
		before := len(b.graph.Edges)
		b.edge(ids[e.From], ids[e.To], "codegraph:"+e.Kind, ids[e.From])
		if len(b.graph.Edges) > before {
			b.graph.Edges[len(b.graph.Edges)-1].Provenance = e.Provenance
		}
	}
	b.gap(r.Source.RepositoryID, "", "partial", "CodeGraph relationships are structural evidence from selected files; unresolved references and portable/deferred files do not prove complete call resolution.")
	if index.Unresolved > 0 {
		b.gap(r.Source.RepositoryID, "", "unresolved", "CodeGraph retained unresolved source references.")
	}
	for _, f := range index.Files {
		if f.State != "native" {
			b.gap(r.Source.RepositoryID, f.Path, "partial", "CodeGraph native Rust extraction was unavailable or deferred; portable extraction may have been used.")
		}
	}
}
