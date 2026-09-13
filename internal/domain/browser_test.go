package domain

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBrowserCredentialsRequireHashesAndCanonicalSecrets(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	valid := BrowserLogin{StateHash: strings.Repeat("a", 64), BindingHash: strings.Repeat("b", 64), Nonce: secret, CodeVerifier: secret, ExpiresAt: time.Now().Add(time.Minute)}
	if err := ValidateBrowserLogin(valid); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*BrowserLogin){
		func(v *BrowserLogin) { v.StateHash = "raw-state" },
		func(v *BrowserLogin) { v.BindingHash = strings.Repeat("A", 64) },
		func(v *BrowserLogin) { v.CodeVerifier = secret + "=" },
		func(v *BrowserLogin) { v.Nonce = strings.Repeat("a", 43) },
		func(v *BrowserLogin) { v.ExpiresAt = time.Time{} },
	} {
		invalid := valid
		edit(&invalid)
		if err := ValidateBrowserLogin(invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("malformed browser login accepted: %v", err)
		}
	}
	session := BrowserSession{TokenHash: valid.StateHash, CSRFToken: secret, Identity: AccessIdentity{Issuer: "https://identity.example.test", Subject: "human-subject"}, ExpiresAt: valid.ExpiresAt}
	if err := ValidateBrowserSession(session); err != nil {
		t.Fatal(err)
	}
	session.CSRFToken = "provider-access-token"
	if err := ValidateBrowserSession(session); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid session secret accepted")
	}
}
