package domain

// GraphArtifactQuery captures the inspected graph and its entire immutable source
// tuple. A related repository is selected inside that graph, never by widening the
// caller's fixed workspace or anchor repository.
type GraphArtifactQuery struct {
	GraphDigest      string `json:"graphDigest"`
	RepositoryID     string `json:"repositoryId"`
	CollectionID     string `json:"collectionId"`
	ReceiptDigest    string `json:"receiptDigest"`
	FullSourceDigest string `json:"fullSourceDigest,omitempty"`
	Path             string `json:"path"`
}

type GraphArtifactResult struct {
	GraphID           string            `json:"graphId"`
	Digest            string            `json:"digest"`
	Source            GraphSourceRecord `json:"source"`
	Artifact          ContextArtifact   `json:"artifact"`
	Coverage          string            `json:"coverage"`
	CoverageTruncated bool              `json:"coverageTruncated"`
}

func ValidateGraphArtifactQuery(q GraphArtifactQuery) error {
	if !IsLowerHex(q.GraphDigest, 64) || ValidateContextPath(q.Path) != nil {
		return ErrInvalidInput
	}
	_, err := NormalizeGraphInput(GraphInput{Sources: []GraphSource{{RepositoryID: q.RepositoryID, CollectionID: q.CollectionID, Digest: q.ReceiptDigest, FullSourceDigest: q.FullSourceDigest}}})
	return err
}
