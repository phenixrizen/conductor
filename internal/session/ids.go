// Package session owns the terminal session model shared by the server and
// the host agent: scrollback, subscriber fan-out, role enforcement, resize
// policy and bounded file reads.
package session

import (
	"crypto/rand"
	"encoding/hex"
)

// IDLen is the length of session, viewer and link identifiers.
const IDLen = 16

// NewID returns 16 lowercase hex characters from 8 random bytes.
func NewID() string {
	var b [IDLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("session: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
