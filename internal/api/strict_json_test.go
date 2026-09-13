package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictCommandRejectsNestedAmbiguityBeforeInterpretation(t *testing.T) {
	for _, input := range []string{`{"tasks":[{"digest":"first","digest":"second"}]}`, `{"tasks":[{"d\u0069gest":"first","digest":"second"}]}`, `{"tasks":[{"actor":"forged"}]}`, `{"tasks":[]}{"tasks":[]}`, `{"tasks":` + strings.Repeat(`[`, 34) + strings.Repeat(`]`, 34) + `}`, "{\"tasks\":[{\"digest\":\"\xff\"}]}"} {
		var command struct {
			Tasks []struct {
				Digest string `json:"digest"`
			} `json:"tasks"`
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(input))
		if err := decodeStrictCommand(w, r, &command); err == nil {
			t.Fatalf("ambiguous command accepted: %q", input)
		}
	}
	var command struct {
		Tasks []struct {
			Digest string `json:"digest"`
		} `json:"tasks"`
	}
	if err := decodeStrictCommand(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(`{"tasks":[{"digest":"inspected"}]}`)), &command); err != nil || len(command.Tasks) != 1 || command.Tasks[0].Digest != "inspected" {
		t.Fatalf("valid exact command rejected: %v", err)
	}
}
