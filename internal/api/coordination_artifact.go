package api

import (
	"context"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/url"
)

func (a *API) coordinationArtifact(w http.ResponseWriter, r *http.Request) {
	s, ok := a.coordination(w, r)
	if !ok {
		return
	}
	reader, ok := s.(interface {
		GetCoordinationArtifact(context.Context, string, domain.CoordinationArtifactQuery) (domain.CoordinationArtifact, error)
	})
	if !ok {
		fail(w, r, domain.ErrUnavailable)
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 3 {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	for key, items := range values {
		if len(items) != 1 || (key != "runDigest" && key != "taskId" && key != "artifactDigest") {
			fail(w, r, domain.ErrInvalidInput)
			return
		}
	}
	q := domain.CoordinationArtifactQuery{RunDigest: values.Get("runDigest"), TaskID: values.Get("taskId"), ArtifactDigest: values.Get("artifactDigest")}
	if domain.ValidateCoordinationArtifactQuery(q) != nil {
		fail(w, r, domain.ErrInvalidInput)
		return
	}
	out, err := reader.GetCoordinationArtifact(r.Context(), r.PathValue("id"), q)
	if err != nil {
		fail(w, r, err)
		return
	}
	write(w, http.StatusOK, out)
}
