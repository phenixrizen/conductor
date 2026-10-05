package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// store is the on-disk layout under dataDir/tls: the ACME account key and
// registration, and one directory per set of identifiers with the
// certificate, its key and its issuer chain. Keys are mode 0600.
type store struct {
	dir string
}

func (s *store) init() error {
	return os.MkdirAll(s.dir, 0o700)
}

// accountKey loads the account's key, or makes one (P-256).
func (s *store) accountKey() (*ecdsa.PrivateKey, error) {
	path := filepath.Join(s.dir, "account.key")
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("%s holds no PEM key", path)
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// account loads the registration kept (ok false when none).
func (s *store) account(v any) (ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "account.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

func (s *store) saveAccount(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.dir, "account.json"), b, 0o600)
}

// certDir is the directory of a set of identifiers: the first sixteen hex
// digits of the SHA-256 of the sorted, comma-joined identifiers.
func (s *store) certDir(ids []string) string {
	sum := sha256.Sum256([]byte(strings.Join(normalize(ids), ",")))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:8]))
}

type meta struct {
	Identifiers []string  `json:"identifiers"`
	IssuedAt    time.Time `json:"issuedAt"`
}

// load reads the certificate kept for ids (ok false when there is none or
// it is incomplete).
func (s *store) load(ids []string) (*Issued, bool) {
	dir := s.certDir(ids)
	cert, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return nil, false
	}
	key, err := os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, false
	}
	issuer, _ := os.ReadFile(filepath.Join(dir, "issuer.pem"))
	return &Issued{CertPEM: cert, KeyPEM: key, IssuerPEM: issuer}, true
}

// save keeps issued for ids.
func (s *store) save(ids []string, issued *Issued) error {
	dir := s.certDir(ids)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "key.pem"), issued.KeyPEM, 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "cert.pem"), issued.CertPEM, 0o600); err != nil {
		return err
	}
	if len(issued.IssuerPEM) > 0 {
		if err := writeFile(filepath.Join(dir, "issuer.pem"), issued.IssuerPEM, 0o600); err != nil {
			return err
		}
	}
	b, _ := json.MarshalIndent(meta{Identifiers: normalize(ids), IssuedAt: time.Now().UTC()}, "", "  ")
	return writeFile(filepath.Join(dir, "meta.json"), b, 0o600)
}

// writeFile writes b to path through a temporary file and a rename.
func writeFile(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
