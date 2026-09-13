package domain

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

const (
	BrowserLoginLifetime        = 5 * time.Minute
	BrowserSessionLifetime      = time.Hour
	MaxBrowserLogins            = 4096
	MaxBrowserSessions          = 10000
	MaxPrincipalBrowserSessions = 20
)

// BrowserLogin retains only hashes of the OAuth state and its browser binding.
// Nonce and PKCE verifier are transient exchange material, consumed once within
// five minutes. They never grant Conductor workspace or repository permissions.
type BrowserLogin struct {
	StateHash    string
	BindingHash  string
	Nonce        string
	CodeVerifier string
	ExpiresAt    time.Time
}

// BrowserSession contains Conductor's own session metadata. Provider access, ID,
// and refresh tokens are not persisted; current authority remains in access tables.
type BrowserSession struct {
	TokenHash string
	CSRFToken string
	Identity  AccessIdentity
	ExpiresAt time.Time
}

func ValidBrowserTokenHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
func validBrowserSecret(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}
func ValidateBrowserLogin(login BrowserLogin) error {
	if !ValidBrowserTokenHash(login.StateHash) || !ValidBrowserTokenHash(login.BindingHash) || !validBrowserSecret(login.Nonce) || !validBrowserSecret(login.CodeVerifier) || login.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	return nil
}
func ValidateBrowserSession(session BrowserSession) error {
	if !ValidBrowserTokenHash(session.TokenHash) || !validBrowserSecret(session.CSRFToken) || !validAccessText(session.Identity.Issuer, 2048) || !validAccessText(session.Identity.Subject, 512) || session.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	return nil
}
