// Package paste is the blob format of a paste invite: an SDP with every ICE
// candidate gathered (no trickle), deflated and base64url-encoded behind a
// version prefix, which people send through any messenger. The viewer's
// browser makes the offer blob (web/app/utils/paste.ts, the same format),
// the session's side answers with one (hostagent.AnswerPaste), and the data
// channel then runs with no server between the two.
package paste

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// BlobPrefix names the blob format.
const BlobPrefix = "cpi1."

// MaxBlob bounds a blob as pasted (an SDP with its candidates is a
// few kilobytes).
const MaxBlob = 64 << 10

// MaxSDP bounds the SDP a blob unpacks to.
const MaxSDP = 256 << 10

// body is what a blob carries.
type blobBody struct {
	Type string `json:"type"` // offer or answer
	SDP  string `json:"sdp"`
}

// EncodeBlob packs an SDP of the type into a blob.
func EncodeBlob(typ, sdp string) (string, error) {
	raw, err := json.Marshal(blobBody{Type: typ, SDP: sdp})
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(raw); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return BlobPrefix + base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

// DecodeBlob unpacks a blob of the type wanted ("" for any): the SDP.
func DecodeBlob(blob, want string) (string, error) {
	blob = strings.TrimSpace(blob)
	if len(blob) > MaxBlob {
		return "", errors.New("paste: blob too long")
	}
	body, ok := strings.CutPrefix(blob, BlobPrefix)
	if !ok {
		return "", errors.New("paste: not a Conductor invite (it starts with " + BlobPrefix + ")")
	}
	packed, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", errors.New("paste: the invite is damaged")
	}
	raw, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(packed)), MaxSDP+1024))
	if err != nil {
		return "", errors.New("paste: the invite is damaged")
	}
	var b blobBody
	if err := json.Unmarshal(raw, &b); err != nil || b.SDP == "" || len(b.SDP) > MaxSDP {
		return "", errors.New("paste: the invite is damaged")
	}
	if want != "" && b.Type != want {
		return "", fmt.Errorf("paste: this is an %s, not an %s", b.Type, want)
	}
	return b.SDP, nil
}
