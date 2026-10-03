package api

import "testing"

func TestInviteFor(t *testing.T) {
	for base, want := range map[string]string{
		"https://switchyard.example.net":      "conductor://switchyard.example.net/join/tok",
		"https://switchyard.example.net:8443": "conductor://switchyard.example.net:8443/join/tok",
		"https://host.example/conductor/":     "conductor://host.example/conductor/join/tok",
		"http://127.0.0.1:18424":              "conductor://127.0.0.1:18424/join/tok?http=1",
		"":                                    "",
		"not a url":                           "",
	} {
		if got := inviteFor(base, "tok"); got != want {
			t.Errorf("%q: %q, want %q", base, got, want)
		}
	}
	if got := inviteFor("https://h", "a b/c"); got != "conductor://h/join/a%20b%2Fc" {
		t.Fatalf("escaping: %q", got)
	}
}
