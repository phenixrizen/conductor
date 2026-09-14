package domain

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Discovery previews count Unicode code points, not bytes. Complete content and
// its digest remain on the immutable revision; previews cannot replace inspection.
const (
	MaxChangeSummaryTitleRunes  = 200
	MaxChangeSummaryIntentRunes = 400
)

type ChangeSummary struct {
	WorkspaceID     string    `json:"workspaceId,omitempty"`
	RepositoryID    string    `json:"repositoryId,omitempty"`
	ID              string    `json:"id"`
	Revision        int64     `json:"revision"`
	Digest          string    `json:"digest"`
	Author          string    `json:"author"`
	CreatedAt       time.Time `json:"createdAt"`
	Approved        bool      `json:"approved"`
	Repository      string    `json:"repository,omitempty"`
	Title           string    `json:"title,omitempty"`
	TitleTruncated  bool      `json:"titleTruncated,omitempty"`
	Intent          string    `json:"intent,omitempty"`
	IntentTruncated bool      `json:"intentTruncated,omitempty"`
}

type ChangePage struct {
	Changes    []ChangeSummary `json:"changes"`
	NextBefore string          `json:"nextBefore,omitempty"`
}

// ChangeCursor uses immutable change creation coordinates so edits do not shift
// entries between pages. Clients pass the encoded cursor back unchanged.
type ChangeCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func ValidateChangeQuery(repository, before string, limit int) (*ChangeCursor, error) {
	if len(repository) > 2048 || !utf8.ValidString(repository) || strings.ContainsRune(repository, '\x00') || limit < 1 || limit > MaxHistoryPageSize || len(before) > 1024 {
		return nil, ErrInvalidInput
	}
	if before == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(before)
	if err != nil {
		return nil, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cursor ChangeCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, ErrInvalidInput
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrInvalidInput
	}
	if cursor.CreatedAt.IsZero() || cursor.ID == "" || len(cursor.ID) > 128 || !utf8.ValidString(cursor.ID) || strings.ContainsAny(cursor.ID, "\r\n\x00") {
		return nil, ErrInvalidInput
	}
	return &cursor, nil
}

func EncodeChangeCursor(createdAt time.Time, id string) (string, error) {
	data, err := json.Marshal(ChangeCursor{CreatedAt: createdAt.UTC(), ID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
