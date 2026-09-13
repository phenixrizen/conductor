// Package temporalconnection defines the operator-selected Temporal transport.
// It never loads ambient Temporal profiles or repository-controlled configuration.
package temporalconnection

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"golang.org/x/sys/unix"
)

const MaxFileBytes = 128 << 10

var name = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
var dnsLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

type Config struct {
	Address, Namespace string
	tls                *tls.Config
}

// Options returns a fresh transport configuration. Callers still resolve and
// recheck RuntimeTarget: a valid TLS server is not proof of retained run history.
func (c Config) Options(identity string, logger log.Logger) client.Options {
	connection := client.ConnectionOptions{MaxPayloadSize: 1 << 20, TLSDisabled: c.tls == nil}
	if c.tls != nil {
		connection.TLS = c.tls.Clone()
	}
	return client.Options{HostPort: c.Address, Namespace: c.Namespace, Identity: identity, Logger: logger, ConnectionOptions: connection}
}

func Load(getenv func(string) string) (Config, error) { return loadAt(getenv, time.Now()) }

func loadAt(getenv func(string) string, now time.Time) (Config, error) {
	c := Config{Address: getenv("CONDUCTOR_TEMPORAL_ADDRESS"), Namespace: getenv("CONDUCTOR_TEMPORAL_NAMESPACE")}
	mode := getenv("CONDUCTOR_TEMPORAL_MODE")
	serverName := getenv("CONDUCTOR_TEMPORAL_SERVER_NAME")
	caPath := getenv("CONDUCTOR_TEMPORAL_CA_FILE")
	certPath := getenv("CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE")
	keyPath := getenv("CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE")
	if !name.MatchString(c.Namespace) {
		return c, errors.New("CONDUCTOR_TEMPORAL_NAMESPACE must be an explicit bounded namespace name")
	}
	if mode != "local" && mode != "remote-tls" {
		return c, errors.New("CONDUCTOR_TEMPORAL_MODE must explicitly be local or remote-tls")
	}
	if mode == "local" && c.Address == "" {
		c.Address = "127.0.0.1:7233"
	}
	host, port, err := net.SplitHostPort(c.Address)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || p < 1 || p > 65535 || len(c.Address) > 300 || !validHost(host) {
		return c, errors.New("CONDUCTOR_TEMPORAL_ADDRESS must be an explicit host and numeric port")
	}
	if mode == "local" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return c, errors.New("local Temporal requires a literal loopback address")
		}
		if serverName != "" || caPath != "" || certPath != "" || keyPath != "" {
			return c, errors.New("local Temporal cannot ignore TLS settings")
		}
		return c, nil
	}
	if !validHost(serverName) {
		return c, errors.New("CONDUCTOR_TEMPORAL_SERVER_NAME must explicitly identify the TLS server")
	}
	if (certPath == "") != (keyPath == "") {
		return c, errors.New("Temporal client certificate and key must be configured together")
	}
	secure := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if caPath != "" {
		data, e := readFile(caPath, false)
		if e != nil {
			return c, errors.New("Temporal CA file is invalid or unavailable")
		}
		certs, e := certificates(data, now)
		if e != nil {
			return c, errors.New("Temporal CA certificates are invalid or expired")
		}
		secure.RootCAs = x509.NewCertPool()
		for _, cert := range certs {
			if !cert.IsCA {
				return c, errors.New("Temporal CA file must contain only CA certificates")
			}
			secure.RootCAs.AddCert(cert)
		}
	} // A nil pool deliberately selects the operating system trust store.
	if certPath != "" {
		certPEM, e := readFile(certPath, false)
		if e != nil {
			return c, errors.New("Temporal client certificate file is invalid or unavailable")
		}
		certs, e := certificates(certPEM, now)
		if e != nil {
			return c, errors.New("Temporal client certificate chain is invalid or expired")
		}
		leaf := certs[0]
		allowed := len(leaf.ExtKeyUsage) == 0
		for _, usage := range leaf.ExtKeyUsage {
			allowed = allowed || usage == x509.ExtKeyUsageClientAuth || usage == x509.ExtKeyUsageAny
		}
		if !allowed {
			return c, errors.New("Temporal client certificate cannot authenticate a TLS client")
		}
		keyPEM, e := readFile(keyPath, true)
		if e != nil {
			return c, errors.New("Temporal client key must be a private regular file owned by the process user or root")
		}
		defer clear(keyPEM)
		block, rest := pem.Decode(keyPEM)
		if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 || !bytes.HasPrefix(bytes.TrimSpace(keyPEM), []byte("-----BEGIN ")) ||
			(block.Type != "PRIVATE KEY" && block.Type != "RSA PRIVATE KEY" && block.Type != "EC PRIVATE KEY") {
			return c, errors.New("Temporal client key must contain exactly one unencrypted PEM private key")
		}
		pair, e := tls.X509KeyPair(certPEM, keyPEM)
		if e != nil {
			return c, errors.New("Temporal client certificate and private key do not match")
		}
		pair.Leaf = leaf
		secure.Certificates = []tls.Certificate{pair}
	}
	c.tls = secure
	return c, nil
}

func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsUnspecified() && !ip.IsMulticast()
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func certificates(data []byte, now time.Time) ([]*x509.Certificate, error) {
	var result []*x509.Certificate
	for len(bytes.TrimSpace(data)) > 0 {
		if len(result) >= 16 || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("invalid PEM")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("invalid PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, errors.New("invalid certificate")
		}
		result = append(result, cert)
		data = rest
	}
	if len(result) == 0 {
		return nil, errors.New("empty certificates")
	}
	return result, nil
}

// Walk directory descriptors without following symlinks; checking then opening
// a path would allow credential replacement between validation and the read.
func readFile(path string, private bool) ([]byte, error) {
	bad := errors.New("invalid Temporal credential file")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return nil, bad
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, bad
	}
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, bad
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), "Temporal credential")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxFileBytes || info.Mode().Perm()&0022 != 0 {
		return nil, bad
	}
	if private {
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || info.Mode().Perm()&0077 != 0 || (stat.Uid != uint32(os.Geteuid()) && stat.Uid != 0) {
			return nil, bad
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil || len(data) > MaxFileBytes {
		return nil, bad
	}
	return data, nil
}
