package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxAssistanceInstructionBytes = 4096
	MaxAssistanceSectionBytes     = 32768
	MaxAssistanceProposalBytes    = 128 << 10
	MaxAssistanceCommandBytes     = 1 << 20
)

type AssistanceInput struct {
	ChangeID         string   `json:"changeId"`
	ExpectedRevision int64    `json:"expectedRevision"`
	ExpectedDigest   string   `json:"expectedDigest"`
	Instruction      string   `json:"instruction"`
	Sections         []string `json:"sections"`
}
type SuggestionInput struct {
	RequestDigest string            `json:"requestDigest"`
	Sections      map[string]string `json:"sections"`
	Note          string            `json:"note,omitempty"`
}
type ApplySuggestionInput struct {
	RequestDigest    string   `json:"requestDigest"`
	SuggestionDigest string   `json:"suggestionDigest"`
	ExpectedRevision int64    `json:"expectedRevision"`
	ExpectedDigest   string   `json:"expectedDigest"`
	Sections         []string `json:"sections"`
}
type DesignAssistance struct {
	ID           string                 `json:"id"`
	WorkspaceID  string                 `json:"workspaceId"`
	RepositoryID string                 `json:"repositoryId"`
	RequesterID  string                 `json:"requesterId"`
	CreatedAt    time.Time              `json:"createdAt"`
	Digest       string                 `json:"digest"`
	Input        AssistanceInput        `json:"input"`
	Base         Revision               `json:"base"`
	Suggestion   *AssistanceSuggestion  `json:"suggestion,omitempty"`
	Application  *AssistanceApplication `json:"application,omitempty"`
}
type AssistanceSuggestion struct {
	Digest    string            `json:"digest"`
	AgentID   string            `json:"agentId"`
	CreatedAt time.Time         `json:"createdAt"`
	Sections  map[string]string `json:"sections"`
	Note      string            `json:"note,omitempty"`
}
type AssistanceApplication struct {
	Digest         string    `json:"digest"`
	AppliedBy      string    `json:"appliedBy"`
	CreatedAt      time.Time `json:"createdAt"`
	Revision       int64     `json:"revision"`
	RevisionDigest string    `json:"revisionDigest"`
	Sections       []string  `json:"sections"`
}
type AssistanceSummary struct {
	ID              string          `json:"id"`
	RequesterID     string          `json:"requesterId"`
	CreatedAt       time.Time       `json:"createdAt"`
	Digest          string          `json:"digest"`
	Input           AssistanceInput `json:"input"`
	HasSuggestion   bool            `json:"hasSuggestion"`
	AppliedRevision int64           `json:"appliedRevision,omitempty"`
}
type AssistancePage struct {
	Requests   []AssistanceSummary `json:"requests"`
	NextBefore string              `json:"nextBefore,omitempty"`
}
type AssistanceListOptions struct {
	ChangeID, Before string
	Limit            int
}

func IsDesignSection(name string) bool {
	switch name {
	case "title", "intent", "scope", "design", "tasks", "verification":
		return true
	}
	return false
}
func ValidateAssistanceSections(sections []string) error {
	if len(sections) < 1 || len(sections) > 6 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, section := range sections {
		if !IsDesignSection(section) || seen[section] {
			return ErrInvalidInput
		}
		seen[section] = true
	}
	return nil
}
func assistanceText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}
func ValidateAssistanceInput(in AssistanceInput) error {
	if ValidateAccessID(in.ChangeID) != nil || in.ExpectedRevision < 1 || !IsLowerHex(in.ExpectedDigest, 64) || !assistanceText(in.Instruction, MaxAssistanceInstructionBytes) || strings.TrimSpace(in.Instruction) == "" {
		return ErrInvalidInput
	}
	return ValidateAssistanceSections(in.Sections)
}
func ValidateSuggestionInput(in SuggestionInput) error {
	if !IsLowerHex(in.RequestDigest, 64) || len(in.Sections) < 1 || len(in.Sections) > 6 || !assistanceText(in.Note, MaxAssistanceInstructionBytes) {
		return ErrInvalidInput
	}
	for field, value := range in.Sections {
		if !IsDesignSection(field) || !assistanceText(value, MaxAssistanceSectionBytes) {
			return ErrInvalidInput
		}
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > MaxAssistanceProposalBytes {
		return ErrInvalidInput
	}
	return nil
}
func ValidateApplySuggestionInput(in ApplySuggestionInput) error {
	if !IsLowerHex(in.RequestDigest, 64) || !IsLowerHex(in.SuggestionDigest, 64) || !IsLowerHex(in.ExpectedDigest, 64) || in.ExpectedRevision < 1 {
		return ErrInvalidInput
	}
	return ValidateAssistanceSections(in.Sections)
}
func ValidateAssistanceListOptions(in AssistanceListOptions) (*ChangeCursor, error) {
	if in.ChangeID != "" && ValidateAccessID(in.ChangeID) != nil {
		return nil, ErrInvalidInput
	}
	cursor, err := ValidateChangeQuery("", in.Before, in.Limit)
	if err != nil {
		return nil, err
	}
	if cursor != nil && !IsLowerHex(cursor.ID, 32) {
		return nil, ErrInvalidInput
	}
	return cursor, nil
}
func ValidateAssistanceBase(in AssistanceInput, base Revision) error {
	if ValidateAssistanceInput(in) != nil {
		return ErrInvalidInput
	}
	if base.ChangeID != in.ChangeID || base.Number != in.ExpectedRevision || base.Digest != in.ExpectedDigest {
		return ErrConflict
	}
	for _, field := range in.Sections {
		if value, present := base.Content[field]; present {
			if _, ok := value.(string); !ok {
				return ErrInvalidInput
			}
		}
	}
	return nil
}
func ValidateAssistanceSuggestion(request DesignAssistance, in SuggestionInput) error {
	if err := ValidateSuggestionInput(in); err != nil {
		return err
	}
	if request.Digest != in.RequestDigest {
		return ErrConflict
	}
	allowed := map[string]bool{}
	for _, key := range request.Input.Sections {
		allowed[key] = true
	}
	for key := range in.Sections {
		if !allowed[key] {
			return ErrInvalidInput
		}
	}
	return nil
}

// MergeAssistanceSections overlays only the selected proposed strings. Unknown
// fields and omitted keys retain their original meaning; no coercion or deletion.
func MergeAssistanceSections(request DesignAssistance, current Revision, in ApplySuggestionInput) (Content, string, error) {
	if err := ValidateApplySuggestionInput(in); err != nil {
		return nil, "", err
	}
	if request.Suggestion == nil || request.Digest != in.RequestDigest || request.Suggestion.Digest != in.SuggestionDigest || current.Number != in.ExpectedRevision || current.Digest != in.ExpectedDigest || current.ChangeID != request.Input.ChangeID || current.Number != request.Input.ExpectedRevision || current.Digest != request.Input.ExpectedDigest {
		return nil, "", ErrConflict
	}
	result := make(Content, len(current.Content))
	for key, value := range current.Content {
		result[key] = value
	}
	for _, key := range in.Sections {
		value, present := request.Suggestion.Sections[key]
		if !present {
			return nil, "", ErrInvalidInput
		}
		result[key] = value
	}
	if err := ValidateContent(result); err != nil {
		return nil, "", err
	}
	envelope, err := json.Marshal(struct {
		ExpectedRevision int64   `json:"expectedRevision"`
		Content          Content `json:"content"`
	}{in.ExpectedRevision, result})
	// json.Encoder adds a newline to the command envelope.
	if err != nil || len(envelope)+1 > MaxAssistanceCommandBytes {
		return nil, "", ErrInvalidInput
	}
	digest, err := Digest(result)
	if err != nil {
		return nil, "", err
	}
	if digest == current.Digest {
		return nil, "", ErrConflict
	}
	return result, digest, nil
}
func DesignAssistanceDigest(value DesignAssistance) (string, error) {
	value.Digest = ""
	value.Suggestion = nil
	value.Application = nil
	return JSONDigest(value)
}
func AssistanceSuggestionDigest(requestDigest string, value AssistanceSuggestion) (string, error) {
	value.Digest = ""
	return JSONDigest(struct {
		RequestDigest string               `json:"requestDigest"`
		Suggestion    AssistanceSuggestion `json:"suggestion"`
	}{requestDigest, value})
}
func AssistanceApplicationDigest(requestDigest, suggestionDigest string, value AssistanceApplication) (string, error) {
	value.Digest = ""
	return JSONDigest(struct {
		RequestDigest    string                `json:"requestDigest"`
		SuggestionDigest string                `json:"suggestionDigest"`
		Application      AssistanceApplication `json:"application"`
	}{requestDigest, suggestionDigest, value})
}

// Exact fields and non-null scalar/map entries prevent encoding/json's case
// folding and null-to-zero behavior from changing the command being confirmed.
func assistanceObject(data []byte, required, optional []string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, ErrInvalidInput
	}
	allowed := map[string]bool{}
	for _, key := range append(required, optional...) {
		allowed[key] = true
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalidInput
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, ErrInvalidInput
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || values[key] != nil {
			return nil, ErrInvalidInput
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, ErrInvalidInput
		}
		values[key] = raw
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalidInput
	}
	for _, key := range required {
		if values[key] == nil {
			return nil, ErrInvalidInput
		}
	}
	return values, nil
}
func (in *AssistanceInput) UnmarshalJSON(data []byte) error {
	type plain AssistanceInput
	var value plain
	if _, err := assistanceObject(data, []string{"changeId", "expectedRevision", "expectedDigest", "instruction", "sections"}, nil); err != nil {
		return err
	}
	if json.Unmarshal(data, &value) != nil {
		return ErrInvalidInput
	}
	*in = AssistanceInput(value)
	return ValidateAssistanceInput(*in)
}
func (in *SuggestionInput) UnmarshalJSON(data []byte) error {
	type plain SuggestionInput
	var value plain
	fields, err := assistanceObject(data, []string{"requestDigest", "sections"}, []string{"note"})
	if err != nil {
		return err
	}
	if _, err = assistanceObject(fields["sections"], nil, []string{"title", "intent", "scope", "design", "tasks", "verification"}); err != nil {
		return err
	}
	if json.Unmarshal(data, &value) != nil {
		return ErrInvalidInput
	}
	*in = SuggestionInput(value)
	return ValidateSuggestionInput(*in)
}
func (in *ApplySuggestionInput) UnmarshalJSON(data []byte) error {
	type plain ApplySuggestionInput
	var value plain
	if _, err := assistanceObject(data, []string{"requestDigest", "suggestionDigest", "expectedRevision", "expectedDigest", "sections"}, nil); err != nil {
		return err
	}
	if json.Unmarshal(data, &value) != nil {
		return ErrInvalidInput
	}
	*in = ApplySuggestionInput(value)
	return ValidateApplySuggestionInput(*in)
}
