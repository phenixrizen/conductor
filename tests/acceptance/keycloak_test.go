package acceptance_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

const keycloakImage = "quay.io/keycloak/keycloak@sha256:ff4257d0d64efbe99ed1ddfaf07765cc3c36dc7518bf8324d41961327f441c54"
const keycloakImageID = "sha256:946db99597ac833a42348d8da2dc6e28be656211758cdb5e84d2e415e158191f"
const keycloakPassword = "Conductor-Synthetic-Qualification-Only-2026"

// All source, passwords, certificates, container storage and realm data here are
// synthetic and owned by the test. Keycloak itself supplies discovery, keys,
// login forms and signed tokens; no mock issuer participates in this acceptance.
func TestKeycloakBrowserQualification(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_KEYCLOAK") != "1" {
		t.Skip("set CONDUCTOR_TEST_KEYCLOAK=1 for owned Keycloak/HTTPS/Chromium/PostgreSQL acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("Keycloak qualification requires an explicit test database")
	}
	app := httptest.NewUnstartedServer(nil)
	t.Cleanup(app.Close)
	origin := "https://" + app.Listener.Addr().String()
	pair, certPEM, keyPEM, pin, roots := keycloakCertificate(t)
	issuerURL, providerClient := startKeycloak(t, origin+"/api/v1/auth/callback", certPEM, keyPEM, roots)
	recorder := &keycloakExchangeRecorder{base: providerClient.Transport}
	providerClient.Transport = recorder
	f := newAccessFixtureWithIssuer(t, &accessIssuer{url: issuerURL}, providerClient, 5*time.Minute)
	browser, e := authn.NewBrowser(f.ctx, authn.BrowserConfig{Issuer: issuerURL, ClientID: browserClientID, ClientSecret: browserClientSecret, RedirectURL: origin + "/api/v1/auth/callback", HTTPClient: providerClient})
	if e != nil {
		t.Fatal("real Keycloak browser discovery", e)
	}
	handler, e := api.NewBrowserAuthenticated(service.NewAuthenticated(f.db), f.verifier, browser, f.db, api.BrowserConfig{Origin: origin, Issuer: issuerURL})
	if e != nil {
		t.Fatal(e)
	}
	dist, e := filepath.Abs("../../apps/web/dist")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dist, "index.html")); e != nil {
		t.Fatal("build the web app before Keycloak qualification")
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	mux.Handle("/", http.FileServer(http.Dir(dist)))
	app.Config.Handler = mux
	app.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	app.StartTLS()
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	reportPath := filepath.Join(t.TempDir(), "qualification.json")
	command := exec.CommandContext(f.ctx, python, "../browser/keycloak.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+origin, "CONDUCTOR_KEYCLOAK_ISSUER="+issuerURL, "CONDUCTOR_KEYCLOAK_PASSWORD="+keycloakPassword, "CONDUCTOR_KEYCLOAK_SPKI="+pin, "CONDUCTOR_KEYCLOAK_REPORT="+reportPath)
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("real Keycloak browser qualification: %v\n%s", e, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	data, e := os.ReadFile(reportPath)
	if e != nil {
		t.Fatal(e)
	}
	var report struct {
		ChangeID                string `json:"changeId"`
		Digest                  string `json:"digest"`
		FailedNonce, FailedPKCE bool
	}
	if json.Unmarshal(data, &report) != nil || report.ChangeID == "" || !report.FailedNonce || !report.FailedPKCE {
		t.Fatal("incomplete browser qualification report")
	}
	ctx := domain.WithAccess(f.ctx, domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: issuerURL, Subject: "subject-reviewer"}, WorkspaceID: "team", RepositoryID: "application"})
	pkg, e := service.NewAuthenticated(f.db).Get(ctx, report.ChangeID)
	if e != nil {
		t.Fatal(e)
	}
	if pkg.Revision.Number != 2 || pkg.Revision.Digest != report.Digest || !pkg.Approved || pkg.Revision.Author != "person-author" {
		t.Fatal("real provider browser did not retain exact independently approved revision")
	}
	var approvals int
	var approver string
	if e := f.sql.QueryRow(f.ctx, `SELECT count(*),min(reviewer) FROM approvals WHERE change_id=$1 AND revision=2`, report.ChangeID).Scan(&approvals, &approver); e != nil {
		t.Fatal(e)
	}
	if approvals != 1 || approver != "person-reviewer" {
		t.Fatal("browser approval identity or duplicate protection failed")
	}
	// Keycloak's selected default access tokens use typ JWT, not RFC9068 at+jwt.
	// Preserve that distinction using the real returned token, never a fabricated one.
	recorder.mu.Lock()
	accessToken, idToken, exchanges, valid := recorder.access, recorder.id, recorder.exchanges, recorder.valid
	recorder.mu.Unlock()
	if exchanges < 4 || !valid || accessToken == "" || idToken == "" {
		t.Fatal("actual confidential PKCE exchange was not observed")
	}
	for _, token := range []string{accessToken, idToken} {
		if _, e := f.verifier.Verify(f.ctx, token); !errors.Is(e, authn.ErrInvalidToken) {
			t.Fatal("OIDC token was accepted by the strict API access-token profile")
		}
		req, _ := http.NewRequestWithContext(f.ctx, "GET", origin+"/api/v1/session", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response, e := providerClient.Do(req)
		if e != nil {
			t.Fatal("API profile check unavailable")
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal("non-RFC9068 provider token reached API")
		}
	}
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		t.Fatal("expected actual signed Keycloak token")
	}
	header, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		t.Fatal("invalid provider token header")
	}
	var h struct {
		Type string `json:"typ"`
	}
	if json.Unmarshal(header, &h) != nil || h.Type != "JWT" {
		t.Fatal("selected Keycloak access-token profile changed")
	}
	t.Log("Qualified Keycloak26.7.3 browser code/PKCE/nonce/session flow; default JWT access tokens and ID tokens correctly rejected by RFC9068 API boundary")
}

func keycloakCertificate(t *testing.T) (tls.Certificate, []byte, []byte, string, *x509.CertPool) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Conductor owned Keycloak qualification"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	pair, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return pair, certPEM, keyPEM, base64.StdEncoding.EncodeToString(hash[:]), roots
}
func startKeycloak(t *testing.T, callback string, cert, key []byte, roots *x509.CertPool) (string, *http.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker, e := exec.LookPath("docker")
	if e != nil {
		t.Fatal("Keycloak qualification requires Docker")
	}
	run := func(args ...string) []byte {
		t.Helper()
		b, e := exec.CommandContext(ctx, docker, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("owned Keycloak Docker command %s failed: %v\n%s", args[0], e, b)
		}
		return b
	}
	if got := strings.TrimSpace(string(run("image", "inspect", keycloakImage, "--format", "{{.Id}} {{.Architecture}}"))); got != keycloakImageID+" amd64" {
		t.Fatal("qualification requires the exact pinned Keycloak26.7.3 linux/amd64 image; run scripts/qualify-keycloak.sh")
	}
	var random [8]byte
	_, _ = rand.Read(random[:])
	name := "conductor-keycloak-" + hex.EncodeToString(random[:])
	network := name + "-network"
	run("network", "create", "--internal", network)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if b, e := exec.CommandContext(cleanup, docker, "network", "rm", network).CombinedOutput(); e != nil {
			t.Errorf("remove owned Keycloak network: %v %s", e, b)
		}
	})
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	t.Cleanup(func() { _ = listener.Close() })
	origin := "https://localhost:" + port
	issuer := origin + "/realms/conductor-qualification"
	run("create", "--name", name, "--network", network, "--memory", "1g", "--cpus", "2", "--pids-limit", "512", "--cap-drop", "ALL", "--entrypoint", "/opt/keycloak/qualification-launcher", keycloakImage, "start-dev", "--http-enabled=false", "--hostname="+origin, "--https-certificate-file=/opt/keycloak/conf/qualification.pem", "--https-certificate-key-file=/opt/keycloak/conf/qualification.key", "--import-realm")
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if b, e := exec.CommandContext(cleanup, docker, "rm", "--force", name).CombinedOutput(); e != nil {
			t.Errorf("remove owned Keycloak container: %v %s", e, b)
		}
	})
	users := []any{}
	for _, user := range []string{"author", "reviewer", "reader", "agent", "other"} {
		users = append(users, map[string]any{"id": "subject-" + user, "username": user, "enabled": true, "emailVerified": true, "email": user + "@example.invalid", "firstName": "Synthetic", "lastName": user, "requiredActions": []string{}, "credentials": []any{map[string]any{"type": "password", "value": keycloakPassword, "temporary": false}}})
	}
	realm := map[string]any{"realm": "conductor-qualification", "enabled": true, "sslRequired": "all", "defaultSignatureAlgorithm": "RS256", "loginWithEmailAllowed": false, "registrationAllowed": false, "resetPasswordAllowed": false, "users": users, "clients": []any{map[string]any{"clientId": browserClientID, "secret": browserClientSecret, "protocol": "openid-connect", "enabled": true, "publicClient": false, "standardFlowEnabled": true, "implicitFlowEnabled": false, "directAccessGrantsEnabled": false, "serviceAccountsEnabled": false, "redirectUris": []string{callback}, "attributes": map[string]string{"pkce.code.challenge.method": "S256", "id.token.signed.response.alg": "RS256"}, "defaultClientScopes": []string{"profile", "email"}}}}
	realmJSON, e := json.Marshal(realm)
	if e != nil {
		t.Fatal(e)
	}
	launcherPath := filepath.Join(t.TempDir(), "qualification-launcher")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", launcherPath, "../providers/keycloak")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build owned provider launcher: %v %s", err, output)
	}
	launcher, err := os.ReadFile(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	for path, data := range map[string][]byte{"opt/keycloak/conf/qualification.pem": cert, "opt/keycloak/conf/qualification.key": key, "opt/keycloak/data/import/conductor-qualification-realm.json": realmJSON, "opt/keycloak/qualification-launcher": launcher} {
		mode := int64(0600)
		if path == "opt/keycloak/qualification-launcher" {
			mode = 0555
		}
		if e := tw.WriteHeader(&tar.Header{Name: path, Mode: mode, Uid: 1000, Gid: 0, Size: int64(len(data)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e := tw.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	copy := exec.CommandContext(ctx, docker, "cp", "-a", "-", name+":/")
	copy.Stdin = &archive
	if b, e := copy.CombinedOutput(); e != nil {
		t.Fatalf("stage owned Keycloak files: %v %s", e, b)
	}
	run("start", name)
	address := strings.TrimSpace(string(run("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name)))
	if ip := net.ParseIP(address); ip == nil || !ip.IsPrivate() {
		t.Fatal("owned Keycloak network has no private endpoint")
	}
	keycloakTCPForward(t, listener, net.JoinHostPort(address, "8443"))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(transport.CloseIdleConnections)
	checks := 0
	for ctx.Err() == nil {
		checks++
		if checks%10 == 0 {
			state, err := exec.CommandContext(ctx, docker, "inspect", "--format", "{{.State.Running}}", name).Output()
			if err != nil || strings.TrimSpace(string(state)) != "true" {
				break
			}
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", issuer+"/.well-known/openid-configuration", nil)
		response, e := client.Do(req)
		if e == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 256<<10))
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				// Check the actual Java process inherited the launcher's restriction.
				status := string(run("exec", name, "/bin/bash", "-c", `while IFS= read -r line; do case "$line" in NoNewPrivs:*) printf '%s\n' "$line";; esac; done < /proc/1/status`))
				if fields := strings.Fields(status); len(fields) != 2 || fields[0] != "NoNewPrivs:" || fields[1] != "1" {
					t.Fatal("owned Keycloak lost no_new_privs")
				}
				return issuer, client
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	logs, _ := exec.Command(docker, "logs", "--tail", "80", name).CombinedOutput()
	t.Fatalf("owned HTTPS Keycloak did not become ready: %s", logs)
	return "", nil
}

type keycloakExchangeRecorder struct {
	base       http.RoundTripper
	mu         sync.Mutex
	access, id string
	exchanges  int
	valid      bool
}

func (r *keycloakExchangeRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	token := strings.HasSuffix(req.URL.Path, "/protocol/openid-connect/token")
	valid := false
	if token {
		body, e := io.ReadAll(io.LimitReader(req.Body, 64<<10))
		if e != nil {
			return nil, e
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		form, e := url.ParseQuery(string(body))
		id, secret, ok := req.BasicAuth()
		valid = e == nil && ok && id == browserClientID && secret == browserClientSecret && form.Get("grant_type") == "authorization_code" && len(form.Get("code_verifier")) >= 43 && strings.HasPrefix(form.Get("redirect_uri"), "https://127.0.0.1:")
	}
	response, e := r.base.RoundTrip(req)
	if e != nil || !token {
		return response, e
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	_ = response.Body.Close()
	if e != nil || len(data) > 256<<10 {
		return nil, errors.New("bounded Keycloak token response unavailable")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exchanges == 0 {
		r.valid = true
	}
	r.exchanges++
	r.valid = r.valid && valid
	if response.StatusCode == 200 {
		var value struct {
			Access string `json:"access_token"`
			ID     string `json:"id_token"`
		}
		if json.Unmarshal(data, &value) == nil {
			r.access, r.id = value.Access, value.ID
		}
	}
	return response, nil
}

// Internal Docker networks deliberately have no published ports. A test-owned
// loopback TCP forwarder transports encrypted bytes to the owned private endpoint;
// Keycloak itself terminates HTTPS and supplies the certificate and OIDC issuer.
func keycloakTCPForward(t *testing.T, listener net.Listener, target string) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	active := map[net.Conn]bool{}
	closed := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			front, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = front.Close()
				return
			}
			active[front] = true
			wg.Add(1)
			mu.Unlock()
			go func() {
				defer wg.Done()
				defer func() { _ = front.Close(); mu.Lock(); delete(active, front); mu.Unlock() }()
				back, err := net.DialTimeout("tcp", target, 3*time.Second)
				if err != nil {
					return
				}
				defer back.Close()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(back, front); _ = back.Close(); close(done) }()
				_, _ = io.Copy(front, back)
				_ = front.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		closed = true
		_ = listener.Close()
		for c := range active {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
}
