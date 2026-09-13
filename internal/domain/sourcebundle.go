package domain

import "time"

const (
	MaxSourceBundleBytes = 32 << 20
	MaxSourceFiles       = 512
	MaxSourceTextBytes   = 4 << 20
)

// SourceBundleData is trusted activity output. Bundle bytes never enter workflow
// payloads, public JSON or a repository command's credentials.
type SourceBundleData struct {
	Commit    string            `json:"commit"`
	Tree      string            `json:"tree"`
	Digest    string            `json:"digest"`
	Bundle    []byte            `json:"-"`
	Artifacts []ContextArtifact `json:"artifacts"`
	FileCount int               `json:"fileCount"`
	Truncated bool              `json:"truncated"`
}
type SourceBundleSummary struct {
	Digest       string    `json:"digest"`
	Tree         string    `json:"tree"`
	Commit       string    `json:"commit"`
	Size         int       `json:"size"`
	FileCount    int       `json:"fileCount"`
	IndexedFiles int       `json:"indexedFiles"`
	Truncated    bool      `json:"truncated"`
	CreatedAt    time.Time `json:"createdAt"`
}
type SourceBundle struct {
	SourceBundleData
	WorkspaceID   string `json:"workspaceId"`
	RepositoryID  string `json:"repositoryId"`
	CollectionID  string `json:"collectionId"`
	ReceiptDigest string `json:"receiptDigest"`
}
