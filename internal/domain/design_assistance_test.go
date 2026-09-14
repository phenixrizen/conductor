package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDesignAssistanceStrictCommands(t *testing.T) {
	digest := strings.Repeat("a", 64)
	goodRequest := `{"changeId":"CHG-fixture","expectedRevision":1,"expectedDigest":"` + digest + `","instruction":"Improve design","sections":["design"]}`
	goodSuggestion := `{"requestDigest":"` + digest + `","sections":{"design":"Suggested"},"note":"Unverified"}`
	goodApply := `{"requestDigest":"` + digest + `","suggestionDigest":"` + digest + `","expectedRevision":1,"expectedDigest":"` + digest + `","sections":["design"]}`
	for _, tc := range []struct {
		name, valid string
		target      func() any
		bad         []string
	}{
		{"request", goodRequest, func() any { return new(AssistanceInput) }, []string{strings.Replace(goodRequest, `"instruction"`, `"Instruction"`, 1), strings.Replace(goodRequest, `"instruction":"Improve design"`, `"instruction":null`, 1), strings.Replace(goodRequest, `"sections":["design"]`, `"sections":["design","design"]`, 1), strings.Replace(goodRequest, `"sections":["design"]`, `"sections":[null]`, 1), strings.Replace(goodRequest, `"instruction":"Improve design"`, `"instruction":"a","instruction":"b"`, 1), strings.Replace(goodRequest, `"instruction":"Improve design"`, `"instruction":" "`, 1)}},
		{"suggestion", goodSuggestion, func() any { return new(SuggestionInput) }, []string{strings.Replace(goodSuggestion, `"design":"Suggested"`, `"design":null`, 1), strings.Replace(goodSuggestion, `"design":"Suggested"`, `"design":"a","design":"b"`, 1), strings.Replace(goodSuggestion, `"design":"Suggested"`, `"Design":"a"`, 1), strings.Replace(goodSuggestion, `"note":"Unverified"`, `"note":null`, 1), strings.Replace(goodSuggestion, `"design":"Suggested"`, `"design":{"text":"a"}`, 1)}},
		{"application", goodApply, func() any { return new(ApplySuggestionInput) }, []string{strings.Replace(goodApply, `"expectedRevision":1`, `"expectedRevision":null`, 1), strings.Replace(goodApply, `"expectedDigest"`, `"ExpectedDigest"`, 1), strings.Replace(goodApply, `"sections":["design"]`, `"sections":["Design"]`, 1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.valid), tc.target()); err != nil {
				t.Fatal(err)
			}
			for _, bad := range append(tc.bad, `null`, strings.TrimSuffix(tc.valid, "}")+`,"actor":"forged"}`, strings.TrimSuffix(tc.valid, "}")+`,"extra":true}`) {
				if err := json.Unmarshal([]byte(bad), tc.target()); err == nil {
					t.Fatalf("ambiguous input accepted: %s", bad)
				}
			}
		})
	}
}
func TestDesignAssistanceBoundsAndSelectedOverlay(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, in := range []SuggestionInput{
		{RequestDigest: digest, Sections: map[string]string{"design": strings.Repeat("x", MaxAssistanceSectionBytes+1)}},
		{RequestDigest: digest, Sections: map[string]string{"design": "\x00"}},
		{RequestDigest: digest, Sections: map[string]string{"design": strings.Repeat("界", MaxAssistanceSectionBytes/3+1)}},
		{RequestDigest: digest, Sections: map[string]string{"design": strings.Repeat("<", MaxAssistanceSectionBytes)}},
	} {
		if ValidateSuggestionInput(in) == nil {
			t.Fatal("oversize or unrepresentable proposal accepted")
		}
	}
	in := AssistanceInput{ChangeID: "CHG-fixture", ExpectedRevision: 1, ExpectedDigest: digest, Instruction: strings.Repeat("x", 4097), Sections: []string{"design"}}
	if ValidateAssistanceInput(in) == nil {
		t.Fatal("oversize instruction accepted")
	}
	base := Revision{ChangeID: "CHG-fixture", Number: 1, Content: Content{"design": "Before", "unknown": map[string]any{"n": 1}, "intent": nil}}
	base.Digest, _ = Digest(base.Content)
	request := DesignAssistance{Digest: digest, Input: AssistanceInput{ChangeID: base.ChangeID, ExpectedRevision: 1, ExpectedDigest: base.Digest, Sections: []string{"design", "scope"}}, Base: base, Suggestion: &AssistanceSuggestion{Digest: digest, Sections: map[string]string{"design": "After", "scope": ""}}}
	apply := ApplySuggestionInput{RequestDigest: digest, SuggestionDigest: digest, ExpectedRevision: 1, ExpectedDigest: base.Digest, Sections: []string{"design"}}
	result, _, err := MergeAssistanceSections(request, base, apply)
	if err != nil || result["design"] != "After" || result["unknown"] == nil {
		t.Fatalf("merge: %+v %v", result, err)
	}
	if _, present := result["scope"]; present {
		t.Fatal("unselected absent key created")
	}
	if value, present := result["intent"]; !present || value != nil {
		t.Fatal("null removed")
	}
	apply.Sections = []string{"title"}
	if _, _, err = MergeAssistanceSections(request, base, apply); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unproposed section: %v", err)
	}
	apply.Sections = []string{"design"}
	base.Content["large"] = strings.Repeat("x", MaxAssistanceCommandBytes)
	if _, _, err = MergeAssistanceSections(request, base, apply); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversize final envelope: %v", err)
	}
}
