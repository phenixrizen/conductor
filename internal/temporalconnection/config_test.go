package temporalconnection

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/contextworkflow"
	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	workflowservicepb "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/durationpb"
)

type fixturePKI struct {
	ca, cert, key []byte
	root          *x509.Certificate
	signer        ed25519.PrivateKey
}

func newPKI(t *testing.T) fixturePKI {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Conductor synthetic CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, root, root, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	p := fixturePKI{ca: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), root: root, signer: key}
	p.cert, p.key = p.issue(t, true, time.Now().Add(time.Hour))
	return p
}
func (p fixturePKI) issue(t *testing.T, clientCert bool, expires time.Time) ([]byte, []byte) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	usage := x509.ExtKeyUsageServerAuth
	if clientCert {
		usage = x509.ExtKeyUsageClientAuth
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(expires.UnixNano()), Subject: pkix.Name{CommonName: "synthetic identity"}, DNSNames: []string{"temporal.example.invalid"}, NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, e := x509.CreateCertificate(rand.Reader, cert, p.root, pub, p.signer)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
}
func put(t *testing.T, dir, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if e := os.WriteFile(path, data, mode); e != nil {
		t.Fatal(e)
	}
	return path
}
func (p fixturePKI) env(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	return map[string]string{"CONDUCTOR_TEMPORAL_MODE": "remote-tls", "CONDUCTOR_TEMPORAL_ADDRESS": "temporal.example.invalid:7233", "CONDUCTOR_TEMPORAL_NAMESPACE": "synthetic-namespace", "CONDUCTOR_TEMPORAL_SERVER_NAME": "temporal.example.invalid", "CONDUCTOR_TEMPORAL_CA_FILE": put(t, dir, "ca.pem", p.ca, 0644), "CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE": put(t, dir, "client.pem", p.cert, 0644), "CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE": put(t, dir, "client.key", p.key, 0600)}
}
func loadEnv(env map[string]string) (Config, error) {
	return Load(func(k string) string { return env[k] })
}
func TestExplicitProfilesAndCredentialValidation(t *testing.T) {
	p := newPKI(t)
	for _, row := range []struct{ name, key, value string }{
		{"mode", "CONDUCTOR_TEMPORAL_MODE", ""}, {"implicit_remote", "CONDUCTOR_TEMPORAL_MODE", "remote"}, {"no_address", "CONDUCTOR_TEMPORAL_ADDRESS", ""}, {"resolver_uri", "CONDUCTOR_TEMPORAL_ADDRESS", "dns:///secret.example:7233"}, {"userinfo", "CONDUCTOR_TEMPORAL_ADDRESS", "synthetic-secret@host:7233"}, {"zero_port", "CONDUCTOR_TEMPORAL_ADDRESS", "example.invalid:0"}, {"wildcard", "CONDUCTOR_TEMPORAL_SERVER_NAME", "*.example.invalid"}, {"no_identity", "CONDUCTOR_TEMPORAL_SERVER_NAME", ""}, {"namespace", "CONDUCTOR_TEMPORAL_NAMESPACE", ""}, {"missing_key", "CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE", ""}, {"relative_ca", "CONDUCTOR_TEMPORAL_CA_FILE", "synthetic-secret.pem"}, {"missing_ca", "CONDUCTOR_TEMPORAL_CA_FILE", "/missing/synthetic-secret"},
	} {
		t.Run(row.name, func(t *testing.T) {
			env := p.env(t)
			env[row.key] = row.value
			_, e := loadEnv(env)
			if e == nil {
				t.Fatal("accepted invalid config")
			}
			if len(e.Error()) > 256 || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatal("raw input reached error")
			}
		})
	}
	t.Run("bad_ca", func(t *testing.T) {
		env := p.env(t)
		_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CA_FILE"], append(p.ca, []byte("secret trailing bytes")...), 0644)
		if _, e := loadEnv(env); e == nil {
			t.Fatal("accepted mixed PEM")
		}
	})
	for _, which := range []string{"ca", "cert", "key"} {
		t.Run("expired_"+which, func(t *testing.T) {
			env := p.env(t)
			if which == "key" {
				other := newPKI(t)
				_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"], other.key, 0600)
			} else if which == "cert" {
				cert, _ := p.issue(t, true, time.Now().Add(-time.Hour))
				_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE"], cert, 0644)
			} else {
				_, e := loadAt(func(k string) string { return env[k] }, time.Now().Add(2*time.Hour))
				if e == nil {
					t.Fatal("accepted expired CA")
				}
				return
			}
			if _, e := loadEnv(env); e == nil {
				t.Fatal("accepted expired/mismatched identity")
			}
		})
	}
	t.Run("wrong_eku", func(t *testing.T) {
		env := p.env(t)
		cert, key := p.issue(t, false, time.Now().Add(time.Hour))
		_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE"], cert, 0644)
		_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"], key, 0600)
		if _, e := loadEnv(env); e == nil {
			t.Fatal("accepted server-only identity")
		}
	})
	t.Run("tls_settings_in_local", func(t *testing.T) {
		env := p.env(t)
		env["CONDUCTOR_TEMPORAL_MODE"] = "local"
		env["CONDUCTOR_TEMPORAL_ADDRESS"] = "127.0.0.1:7233"
		if _, e := loadEnv(env); e == nil {
			t.Fatal("silently ignored TLS")
		}
	})
	t.Run("system_roots", func(t *testing.T) {
		env := p.env(t)
		for _, key := range []string{"CONDUCTOR_TEMPORAL_CA_FILE", "CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE", "CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"} {
			env[key] = ""
		}
		c, e := loadEnv(env)
		if e != nil || c.tls.RootCAs != nil || len(c.tls.Certificates) != 0 {
			t.Fatal("explicit server TLS unavailable", e)
		}
	})
	c, e := loadEnv(p.env(t))
	if e != nil {
		t.Fatal(e)
	}
	options := c.Options("synthetic-worker", quietLogger{})
	if options.ConnectionOptions.TLS.InsecureSkipVerify || options.ConnectionOptions.TLS.MinVersion != tls.VersionTLS12 || options.ConnectionOptions.TLSDisabled || options.ConnectionOptions.MaxPayloadSize != 1<<20 {
		t.Fatal("transport weakened")
	}
	options.ConnectionOptions.TLS.ServerName = "different.invalid"
	if c.Options("synthetic-worker", quietLogger{}).ConnectionOptions.TLS.ServerName != "temporal.example.invalid" {
		t.Fatal("mutable TLS config shared")
	}
	// SDK environment config is deliberately not invoked, even when present.
	t.Setenv("TEMPORAL_ADDRESS", "synthetic-secret.invalid:1")
	t.Setenv("TEMPORAL_API_KEY", "synthetic-secret")
	local, e := loadEnv(map[string]string{"CONDUCTOR_TEMPORAL_MODE": "local", "CONDUCTOR_TEMPORAL_NAMESPACE": "explicit"})
	if e != nil || local.Address != "127.0.0.1:7233" || !local.Options("worker", quietLogger{}).ConnectionOptions.TLSDisabled {
		t.Fatal("local profile changed", e)
	}
}
func TestBoundedFilesRejectSymlinksFIFOAndReadablePrivateKeys(t *testing.T) {
	p := newPKI(t)
	for _, kind := range []string{"symlink", "parent_symlink", "fifo", "oversize", "group_read", "world_write", "directory"} {
		t.Run(kind, func(t *testing.T) {
			env := p.env(t)
			path := env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"]
			switch kind {
			case "symlink":
				target := path + ".target"
				_ = os.Rename(path, target)
				_ = os.Symlink(target, path)
			case "parent_symlink":
				dir := filepath.Dir(path)
				link := dir + "-link"
				if e := os.Symlink(dir, link); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = os.Remove(link) })
				env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"] = filepath.Join(link, "client.key")
			case "fifo":
				_ = os.Remove(path)
				if e := unix.Mkfifo(path, 0600); e != nil {
					t.Fatal(e)
				}
			case "oversize":
				_ = os.WriteFile(path, []byte(strings.Repeat("x", MaxFileBytes+1)), 0600)
			case "group_read":
				_ = os.Chmod(path, 0640)
			case "world_write":
				_ = os.Chmod(env["CONDUCTOR_TEMPORAL_CA_FILE"], 0666)
			case "directory":
				_ = os.Remove(path)
				_ = os.Mkdir(path, 0700)
			}
			if _, e := loadEnv(env); e == nil {
				t.Fatal("accepted unsafe credential file")
			}
		})
	}
}

type quietLogger struct{}

func (quietLogger) Debug(string, ...interface{}) {}
func (quietLogger) Info(string, ...interface{})  {}
func (quietLogger) Warn(string, ...interface{})  {}
func (quietLogger) Error(string, ...interface{}) {}

type tlsService struct {
	workflowservicepb.UnimplementedWorkflowServiceServer
	calls    atomic.Int64
	replaced atomic.Bool
}

func (s *tlsService) GetSystemInfo(context.Context, *workflowservicepb.GetSystemInfoRequest) (*workflowservicepb.GetSystemInfoResponse, error) {
	s.calls.Add(1)
	return &workflowservicepb.GetSystemInfoResponse{}, nil
}
func (s *tlsService) GetClusterInfo(context.Context, *workflowservicepb.GetClusterInfoRequest) (*workflowservicepb.GetClusterInfoResponse, error) {
	id := "cluster-original"
	if s.replaced.Load() {
		id = "cluster-replacement"
	}
	return &workflowservicepb.GetClusterInfoResponse{ClusterId: id}, nil
}
func (s *tlsService) DescribeNamespace(context.Context, *workflowservicepb.DescribeNamespaceRequest) (*workflowservicepb.DescribeNamespaceResponse, error) {
	return &workflowservicepb.DescribeNamespaceResponse{NamespaceInfo: &namespacepb.NamespaceInfo{Name: "synthetic-namespace", Id: "namespace-original", State: enumspb.NAMESPACE_STATE_REGISTERED}, Config: &namespacepb.NamespaceConfig{WorkflowExecutionRetentionTtl: durationpb.New(24 * time.Hour)}}, nil
}
func (p fixturePKI) serverTLS(t *testing.T, mutual bool) *tls.Config {
	cert, key := p.issue(t, false, time.Now().Add(time.Hour))
	pair, e := tls.X509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	if mutual {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(p.ca)
		config.ClientCAs = pool
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return config
}
func TestRealTLSGRPCRejectsWrongIdentityAndPreservesRuntimeBinding(t *testing.T) {
	p := newPKI(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	service := &tlsService{}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(p.serverTLS(t, true))))
	workflowservicepb.RegisterWorkflowServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	env := p.env(t)
	env["CONDUCTOR_TEMPORAL_ADDRESS"] = listener.Addr().String()
	c, e := loadEnv(env)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	engine, e := client.DialContext(ctx, c.Options("synthetic-worker", quietLogger{}))
	if e != nil {
		t.Fatal(e)
	}
	defer engine.Close()
	target, e := contextworkflow.RuntimeTarget(ctx, engine, c.Namespace, c.Address, contextworkflow.TaskQueue)
	if e != nil {
		t.Fatal(e)
	}
	bound, e := contextworkflow.NewBoundRuntime(engine, c.Namespace, c.Address, contextworkflow.TaskQueue, target)
	if e != nil {
		t.Fatal(e)
	}
	service.replaced.Store(true)
	_, e = bound.Lookup(ctx, contextworkflow.WorkflowName+"/"+strings.Repeat("a", 32), "", contextworkflow.Reference{ID: strings.Repeat("a", 32), Binding: strings.Repeat("b", 32)})
	if e != contextworkflow.ErrBindingMismatch {
		t.Fatalf("TLS hid replaced cluster: %v", e)
	}
	for _, kind := range []string{"wrong_server_name", "wrong_ca", "no_client_certificate", "untrusted_client_certificate"} {
		t.Run(kind, func(t *testing.T) {
			env := p.env(t)
			env["CONDUCTOR_TEMPORAL_ADDRESS"] = listener.Addr().String()
			switch kind {
			case "wrong_server_name":
				env["CONDUCTOR_TEMPORAL_SERVER_NAME"] = "other.invalid"
			case "wrong_ca":
				other := newPKI(t)
				_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CA_FILE"], other.ca, 0644)
			case "no_client_certificate":
				env["CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE"] = ""
				env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"] = ""
			case "untrusted_client_certificate":
				other := newPKI(t)
				_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE"], other.cert, 0644)
				_ = os.WriteFile(env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"], other.key, 0600)
			}
			c, e := loadEnv(env)
			if e != nil {
				t.Fatal(e)
			}
			before := service.calls.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			bad, e := client.DialContext(ctx, c.Options("synthetic-worker", quietLogger{}))
			if e == nil {
				bad.Close()
				t.Fatal("invalid TLS identity reached Temporal")
			}
			if service.calls.Load() != before {
				t.Fatal("unauthenticated application request reached Temporal")
			}
		})
	}
}

func TestServerTLSAndPlaintextCannotDowngradeEachOther(t *testing.T) {
	p := newPKI(t)
	for _, serverTLS := range []bool{true, false} {
		t.Run(map[bool]string{true: "server-tls", false: "plaintext"}[serverTLS], func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			var options []grpc.ServerOption
			if serverTLS {
				options = append(options, grpc.Creds(credentials.NewTLS(p.serverTLS(t, false))))
			}
			service := &tlsService{}
			server := grpc.NewServer(options...)
			workflowservicepb.RegisterWorkflowServiceServer(server, service)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			env := p.env(t)
			env["CONDUCTOR_TEMPORAL_ADDRESS"] = listener.Addr().String()
			env["CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE"] = ""
			env["CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE"] = ""
			secure, e := loadEnv(env)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			engine, e := client.DialContext(ctx, secure.Options("test-worker", quietLogger{}))
			if serverTLS {
				if e != nil {
					t.Fatal("server TLS unavailable", e)
				}
				engine.Close()
			} else {
				if e == nil {
					engine.Close()
					t.Fatal("remote TLS downgraded to plaintext")
				}
				if service.calls.Load() != 0 {
					t.Fatal("TLS profile sent plaintext application data")
				}
			}
			if serverTLS {
				local, e := loadEnv(map[string]string{"CONDUCTOR_TEMPORAL_MODE": "local", "CONDUCTOR_TEMPORAL_ADDRESS": listener.Addr().String(), "CONDUCTOR_TEMPORAL_NAMESPACE": "synthetic-namespace"})
				if e != nil {
					t.Fatal(e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				engine, e := client.DialContext(ctx, local.Options("test-worker", quietLogger{}))
				if e == nil {
					engine.Close()
					t.Fatal("plaintext profile reached TLS service")
				}
			}
		})
	}
}
