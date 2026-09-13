package api

import (
	"context"

	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
)

type graphArtifactService interface {
	GetRepositoryGraphArtifact(context.Context, string, domain.GraphArtifactQuery) (domain.GraphArtifactResult, error)
}

func (a *API) getRepositoryGraphArtifact(w http.ResponseWriter, r *http.Request) {
	base, ok := a.graphs(w, r)
	if !ok {
		return
	}
	service, ok := base.(graphArtifactService)
	if !ok {
		fail(w, r, domain.ErrUnavailable)
		return
	}
	values, err := graphQueryValues(r, "graphDigest", "repositoryId", "collectionId", "receiptDigest", "fullSourceDigest", "path")
	if err != nil {
		fail(w, r, err)
		return
	}
	q := domain.GraphArtifactQuery{GraphDigest: values.Get("graphDigest"), RepositoryID: values.Get("repositoryId"), CollectionID: values.Get("collectionId"), ReceiptDigest: values.Get("receiptDigest"), FullSourceDigest: values.Get("fullSourceDigest"), Path: values.Get("path")}
	result, err := service.GetRepositoryGraphArtifact(r.Context(), r.PathValue("id"), q)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, result)
}
