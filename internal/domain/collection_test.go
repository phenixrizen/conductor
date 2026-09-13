package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCollectionInputCanonicalizesWithoutChangingCallerSelection(t *testing.T) {
	input := CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"docs/design.md", "file,with,comma.md", "README.md"}}
	normalized, err := NormalizeCollectionInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Paths, []string{"docs/design.md", "file,with,comma.md", "README.md"}) ||
		!reflect.DeepEqual(normalized.Paths, []string{"README.md", "docs/design.md", "file,with,comma.md"}) {
		t.Fatalf("canonicalization mutated the caller or lost literal paths: input=%+v normalized=%+v", input, normalized)
	}
	permutation, err := NormalizeCollectionInput(CollectionInput{Commit: input.Commit, Paths: []string{"file,with,comma.md", "README.md", "docs/design.md"}})
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := JSONDigest(normalized)
	secondDigest, _ := JSONDigest(permutation)
	if firstDigest != secondDigest {
		t.Fatal("path selection order changed canonical input identity")
	}
}

func TestCollectionInputRejectsUnpinnedOrUnboundedSelection(t *testing.T) {
	for _, commit := range []string{"", "main", "abc123", strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 64)} {
		if _, err := NormalizeCollectionInput(CollectionInput{Commit: commit, Paths: []string{"README.md"}}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("unsupported commit %q accepted: %v", commit, err)
		}
	}
	for _, paths := range [][]string{
		nil, {"README.md", "README.md"}, {"README.md", "docs/design.md", "README.md"},
		{""}, {"."}, {"../README.md"}, {"docs/../README.md"}, {"/etc/passwd"}, {"docs\\design.md"},
		{"-option"}, {"docs//design.md"}, {"docs/design.md\n"}, {"README\x00.md"}, {strings.Repeat("a", 1025)},
	} {
		if _, err := NormalizeCollectionInput(CollectionInput{Commit: strings.Repeat("a", 40), Paths: paths}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("invalid path selection %#v accepted: %v", paths, err)
		}
	}
	paths := make([]string, MaxContextArtifacts)
	for i := range paths {
		paths[i] = fmt.Sprintf("docs/file-%02d.md", i)
	}
	if _, err := NormalizeCollectionInput(CollectionInput{Commit: strings.Repeat("a", 40), Paths: paths}); err != nil {
		t.Fatalf("exact path-count limit rejected: %v", err)
	}
	if _, err := NormalizeCollectionInput(CollectionInput{Commit: strings.Repeat("a", 40), Paths: append(paths, "one-too-many.md")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("path count beyond bound accepted")
	}
}

func TestCollectionIdempotencyKeyHasUnambiguousBoundedHeaderRepresentation(t *testing.T) {
	for _, tc := range []struct {
		key   string
		valid bool
	}{
		{"request-1", true}, {strings.Repeat("x", 128), true}, {"!key:operator|selected~", true},
		{"", false}, {strings.Repeat("x", 129), false}, {"one,two", false}, {" key", false},
		{"key ", false}, {"one two", false}, {"key\tvalue", false}, {"key\nvalue", false},
		{"key\rvalue", false}, {"key\x00value", false}, {"key\x7fvalue", false}, {"cl\u00e9", false},
	} {
		if err := ValidateCollectionKey(tc.key); (err == nil) != tc.valid {
			t.Errorf("key=%q valid=%v error=%v", tc.key, tc.valid, err)
		}
	}
}

func collectionTestSource() ContextSource {
	return ContextSource{WorkspaceID: "team", RepositoryID: "application", Provider: "github", Host: "github.com", ProviderID: "101", Locator: "synthetic/application", Profile: "github-rest/2026-03-10", IntegrationVersion: 1}
}

func TestContextSourceUsesOnlySupportedCanonicalProviderProfiles(t *testing.T) {
	github := collectionTestSource()
	gitlab := github
	gitlab.Provider, gitlab.Host, gitlab.Profile, gitlab.Locator = "gitlab", "gitlab.com", "gitlab-rest/v4-19.3", ""
	for _, source := range []ContextSource{github, gitlab} {
		if err := ValidateContextSource(source); err != nil {
			t.Fatalf("documented source profile rejected: %+v %v", source, err)
		}
	}
	for name, change := range map[string]func(*ContextSource){
		"no workspace":           func(s *ContextSource) { s.WorkspaceID = "" },
		"no repository":          func(s *ContextSource) { s.RepositoryID = "" },
		"no integration version": func(s *ContextSource) { s.IntegrationVersion = 0 },
		"arbitrary host":         func(s *ContextSource) { s.Host = "other.example.test" },
		"host URL":               func(s *ContextSource) { s.Host = "https://github.com" },
		"mismatched provider":    func(s *ContextSource) { s.Provider = "gitlab" },
		"unknown profile":        func(s *ContextSource) { s.Profile = "github-rest/latest" },
		"leading zero ID":        func(s *ContextSource) { s.ProviderID = "0101" },
		"signed ID":              func(s *ContextSource) { s.ProviderID = "+101" },
		"zero ID":                func(s *ContextSource) { s.ProviderID = "0" },
		"oversized ID":           func(s *ContextSource) { s.ProviderID = "9223372036854775808" },
		"locator URL":            func(s *ContextSource) { s.Locator = "https://github.com/synthetic/application" },
		"locator traversal":      func(s *ContextSource) { s.Locator = "../application" },
		"locator query":          func(s *ContextSource) { s.Locator = "synthetic/application?other" },
		"locator fragment":       func(s *ContextSource) { s.Locator = "synthetic/application#other" },
		"locator credentials":    func(s *ContextSource) { s.Locator = "user:secret/application" },
	} {
		t.Run(name, func(t *testing.T) {
			source := collectionTestSource()
			change(&source)
			if err := ValidateContextSource(source); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("unsafe or unsupported source accepted: %+v err=%v", source, err)
			}
		})
	}
	gitlab.Locator = "synthetic/application"
	if err := ValidateContextSource(gitlab); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("GitLab accepted a locator instead of its stable project ID")
	}
}

func TestCollectionBindingKeepsIdentityAcrossEnablementAndSecretRotation(t *testing.T) {
	initial := ContextIntegration{Source: collectionTestSource(), CredentialID: "operator-secret-ref", Enabled: true}
	first, err := ContextBindingDigest(initial)
	if err != nil {
		t.Fatal(err)
	}
	changed := initial
	changed.Enabled, changed.Source.IntegrationVersion = false, 99
	second, err := ContextBindingDigest(changed)
	if err != nil || first != second {
		t.Fatalf("enablement/version change altered canonical binding: %s %s %v", first, second, err)
	}
	if initial.Source.IntegrationVersion != 1 || !initial.Enabled {
		t.Fatal("binding digest mutated the caller's integration")
	}
	for name, update := range map[string]func(*ContextIntegration){
		"workspace":            func(v *ContextIntegration) { v.Source.WorkspaceID = "other-team" },
		"repository":           func(v *ContextIntegration) { v.Source.RepositoryID = "other-repository" },
		"provider":             func(v *ContextIntegration) { v.Source.Provider = "gitlab" },
		"host":                 func(v *ContextIntegration) { v.Source.Host = "other.example.test" },
		"stable ID":            func(v *ContextIntegration) { v.Source.ProviderID = "102" },
		"locator":              func(v *ContextIntegration) { v.Source.Locator = "synthetic/other" },
		"profile":              func(v *ContextIntegration) { v.Source.Profile = "other-profile" },
		"credential reference": func(v *ContextIntegration) { v.CredentialID = "different-ref" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := initial
			update(&changed)
			digest, err := ContextBindingDigest(changed)
			if err != nil || digest == first {
				t.Fatalf("canonical binding change did not change identity: %v", err)
			}
		})
	}
}

func collectionReceiptFixture(t *testing.T) (Collection, CollectionReceipt) {
	t.Helper()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	text := "# Synthetic design\nRead this exact commit.\n"
	hash := sha256.Sum256([]byte(text))
	collection := Collection{ID: strings.Repeat("c", 32), WorkspaceID: "team", RepositoryID: "application", RequesterID: "engineer", Input: CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"README.md", "docs/missing.md"}}, Source: collectionTestSource(), CreatedAt: now}
	snapshot := RepositoryContext{SchemaVersion: 2, Repository: "synthetic/application", Commit: collection.Input.Commit, RequestedRef: collection.Input.Commit, CollectedAt: now, Collector: RemoteContextCollector, CollectionID: collection.ID, Source: &collection.Source,
		Artifacts: []ContextArtifact{
			{Path: "README.md", State: "collected", BlobOID: strings.Repeat("b", 40), Digest: hex.EncodeToString(hash[:]), Text: &text},
			{Path: "docs/missing.md", State: "missing", Message: "Not present at this commit"},
		}}
	digest, err := JSONDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return collection, CollectionReceipt{ID: collection.ID, Digest: digest, Snapshot: snapshot, CreatedAt: now}
}

func TestCollectionReceiptRequiresTheExactRemoteRequestAndCompleteArtifactFacts(t *testing.T) {
	collection, receipt := collectionReceiptFixture(t)
	if err := ValidateCollectionReceipt(receipt, collection); err != nil {
		t.Fatalf("valid explicit partial coverage rejected: %v", err)
	}
	for name, change := range map[string]func(*CollectionReceipt){
		"different receipt ID":    func(r *CollectionReceipt) { r.ID = strings.Repeat("d", 32) },
		"different embedded ID":   func(r *CollectionReceipt) { r.Snapshot.CollectionID = strings.Repeat("d", 32) },
		"different commit":        func(r *CollectionReceipt) { r.Snapshot.Commit = strings.Repeat("d", 40) },
		"different requested ref": func(r *CollectionReceipt) { r.Snapshot.RequestedRef = "main" },
		"different workspace": func(r *CollectionReceipt) {
			source := *r.Snapshot.Source
			source.WorkspaceID = "other-team"
			r.Snapshot.Source = &source
		},
		"different repository": func(r *CollectionReceipt) {
			source := *r.Snapshot.Source
			source.RepositoryID = "other-application"
			r.Snapshot.Source = &source
		},
		"different integration version": func(r *CollectionReceipt) {
			source := *r.Snapshot.Source
			source.IntegrationVersion++
			r.Snapshot.Source = &source
		},
		"missing source":   func(r *CollectionReceipt) { r.Snapshot.Source = nil },
		"missing artifact": func(r *CollectionReceipt) { r.Snapshot.Artifacts = r.Snapshot.Artifacts[:1] },
		"different artifact order": func(r *CollectionReceipt) {
			r.Snapshot.Artifacts[0], r.Snapshot.Artifacts[1] = r.Snapshot.Artifacts[1], r.Snapshot.Artifacts[0]
		},
		"different literal path":       func(r *CollectionReceipt) { r.Snapshot.Artifacts[0].Path = "other.md" },
		"modified collected text":      func(r *CollectionReceipt) { value := "modified text"; r.Snapshot.Artifacts[0].Text = &value },
		"missing gap explanation":      func(r *CollectionReceipt) { r.Snapshot.Artifacts[1].Message = "" },
		"text on uncollected artifact": func(r *CollectionReceipt) { value := "not collected"; r.Snapshot.Artifacts[1].Text = &value },
		"local snapshot masquerading as receipt": func(r *CollectionReceipt) {
			r.Snapshot.SchemaVersion = 1
			r.Snapshot.Collector = RepositoryContextCollector
		},
	} {
		t.Run(name, func(t *testing.T) {
			collection, receipt := collectionReceiptFixture(t)
			change(&receipt)
			// A caller can recompute a digest. Consistency alone must not allow a
			// receipt to change request identity or turn missing source into text.
			receipt.Digest, _ = JSONDigest(receipt.Snapshot)
			if err := ValidateCollectionReceipt(receipt, collection); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("inconsistent receipt accepted: %v", err)
			}
		})
	}
	receipt.Digest = strings.Repeat("f", 64)
	if err := ValidateCollectionReceipt(receipt, collection); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("receipt digest did not bind the persisted snapshot")
	}
}

func TestVersionOneContextKeepsEveryCaseAliasOfNewRemoteFieldsAsAnExtension(t *testing.T) {
	for _, alias := range []string{"source", "Source", "SOURCE", "sOuRcE", "ſource", "collectionId", "CollectionID", "COLLECTIONID", "collectionid"} {
		t.Run(alias, func(t *testing.T) {
			for _, extension := range []any{true, []any{"legacy"}, map[string]any{"legacy": true}, "legacy extension"} {
				content := Content{"repositoryContext": map[string]any{
					"schemaVersion": 1, "repository": "synthetic/local", "commit": strings.Repeat("a", 40), "requestedRef": "main",
					"collectedAt": "2026-09-13T12:00:00Z", "collector": RepositoryContextCollector,
					"artifacts": []any{map[string]any{"path": "README.md", "state": "missing", "message": "Not present at this commit"}},
					alias:       extension,
				}}
				before, err := Digest(content)
				if err != nil {
					t.Fatalf("legacy extension was reinterpreted as a typed remote field: %v", err)
				}
				if err := ValidateContent(content); err != nil {
					t.Fatalf("valid unchanged v1 document rejected: %v", err)
				}
				after, err := Digest(content)
				if err != nil || before != after || !reflect.DeepEqual(content["repositoryContext"].(map[string]any)[alias], extension) {
					t.Fatal("v1 validation discarded an extension or changed the immutable digest")
				}
			}
		})
	}
}
