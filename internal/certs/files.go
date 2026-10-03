package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"
)

// loadFiles reads the configured pair.
func (m *Manager) loadFiles() error {
	cert, err := tls.LoadX509KeyPair(m.o.CertFile, m.o.KeyFile)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("tls: %s: %w", m.o.CertFile, err)
	}
	cert.Leaf = leaf
	m.mu.Lock()
	m.cur, m.leaf, m.lastErr = &cert, leaf, ""
	m.fileTime = m.filesTime()
	m.mu.Unlock()
	return nil
}

// filesTime is the later modification time of the two files.
func (m *Manager) filesTime() time.Time {
	var t time.Time
	for _, p := range []string{m.o.CertFile, m.o.KeyFile} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

// reloadFilesIfChanged re-reads the pair when either file changed; a pair
// that fails to load keeps the one served and records the error.
func (m *Manager) reloadFilesIfChanged() error {
	m.mu.Lock()
	last := m.fileTime
	m.mu.Unlock()
	if !m.filesTime().After(last) {
		return nil
	}
	if err := m.loadFiles(); err != nil {
		m.mu.Lock()
		m.lastErr = err.Error()
		m.fileTime = m.filesTime()
		m.mu.Unlock()
		return err
	}
	m.log.Info("tls: certificate files re-read", "certFile", m.o.CertFile)
	return nil
}
