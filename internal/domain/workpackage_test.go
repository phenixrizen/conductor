package domain

import "testing"

func TestDigestDeterministic(t *testing.T) {
	a, _ := Digest(Content{"b": 2, "a": "one"})
	b, _ := Digest(Content{"a": "one", "b": 2})
	if a != b {
		t.Fatalf("digests differ: %s != %s", a, b)
	}
}

func TestApprovalRules(t *testing.T) {
	r := Revision{Number: 2, Digest: "digest", Author: "author"}
	if err := ValidateApproval(r, 1, "digest", "reviewer"); err != ErrStaleApproval {
		t.Fatalf("got %v", err)
	}
	if err := ValidateApproval(r, 2, "digest", "reviewer"); err != ErrNotSubmitted {
		t.Fatalf("got %v", err)
	}
}
