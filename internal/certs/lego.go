package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/tlsalpn01"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/providers/dns/exec"
	"github.com/go-acme/lego/v5/providers/dns/httpreq"
	"github.com/go-acme/lego/v5/registration"
)

// ACMEOptions say which CA to ask and how.
type ACMEOptions struct {
	Email string
	// Directory is the ACME directory URL; Let's Encrypt's when empty.
	Directory string
	Challenge string
	// DNSProvider and DNSEnv configure dns-01: cloudflare, exec or httpreq,
	// with the settings under lego's variable names.
	DNSProvider string
	DNSEnv      map[string]string
	// HTTPClient talks to the CA; nil is lego's default. A test gives one
	// that trusts Pebble.
	HTTPClient *http.Client
}

// acmeUser is the account lego acts for.
type acmeUser struct {
	email string
	key   *ecdsa.PrivateKey
	reg   *acme.ExtendedAccount
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *acme.ExtendedAccount  { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.Signer            { return u.key }
func (u *acmeUser) setRegistration(r *acme.ExtendedAccount) { u.reg = r }
func (u *acmeUser) registered() bool                        { return u.reg != nil && u.reg.Location != "" }

// legoIssuer obtains certificates through lego: one account in the store,
// registered once; the challenge answered by the manager.
type legoIssuer struct {
	m  *Manager
	o  ACMEOptions
	st *store

	mu     sync.Mutex
	client *lego.Client
}

func newLegoIssuer(m *Manager, o ACMEOptions, st *store) *legoIssuer {
	return &legoIssuer{m: m, o: o, st: st}
}

// clientFor builds the lego client once: the account key from the store,
// the registration made or loaded, the challenge provider set.
func (l *legoIssuer) clientFor(ctx context.Context) (*lego.Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != nil {
		return l.client, nil
	}
	key, err := l.st.accountKey()
	if err != nil {
		return nil, fmt.Errorf("acme account key: %w", err)
	}
	user := &acmeUser{email: l.o.Email, key: key}
	var reg acme.ExtendedAccount
	if ok, err := l.st.account(&reg); err != nil {
		return nil, fmt.Errorf("acme account: %w", err)
	} else if ok {
		user.reg = &reg
	}
	cfg := lego.NewConfig(user)
	if l.o.Directory != "" {
		cfg.CADirURL = l.o.Directory
	}
	cfg.UserAgent = "conductor"
	if l.o.HTTPClient != nil {
		cfg.HTTPClient = l.o.HTTPClient
	}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("acme client: %w", err)
	}
	switch l.o.Challenge {
	case ChallengeTLSALPN, "":
		err = client.Challenge.SetTLSALPN01Provider(alpnProvider{l.m})
	case ChallengeHTTP:
		err = client.Challenge.SetHTTP01Provider(httpProvider{l.m})
	case ChallengeDNS:
		var p challenge.Provider
		p, err = newDNSProvider(l.o.DNSProvider, l.o.DNSEnv)
		if err == nil {
			err = client.Challenge.SetDNS01Provider(p)
		}
	default:
		err = fmt.Errorf("unknown challenge %q", l.o.Challenge)
	}
	if err != nil {
		return nil, fmt.Errorf("acme challenge: %w", err)
	}
	if !user.registered() {
		r, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("acme registration: %w", err)
		}
		user.setRegistration(r)
		if err := l.st.saveAccount(r); err != nil {
			return nil, fmt.Errorf("acme account: %w", err)
		}
	}
	l.client = client
	return client, nil
}

// Issue orders a certificate for ids with a fresh key.
func (l *legoIssuer) Issue(ctx context.Context, ids []string, profile string) (*Issued, error) {
	client, err := l.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	res, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: ids, Bundle: true, Profile: profile, KeyType: certcrypto.EC256})
	if err != nil {
		return nil, err
	}
	return &Issued{CertPEM: res.Certificate, KeyPEM: res.PrivateKey, IssuerPEM: res.IssuerCertificate}, nil
}

// RenewalWindow asks the CA's renewal information (RFC 9773) for leaf.
func (l *legoIssuer) RenewalWindow(ctx context.Context, leaf *x509.Certificate) (start, end time.Time, ok bool) {
	l.mu.Lock()
	client := l.client
	l.mu.Unlock()
	if client == nil {
		return start, end, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ri, err := client.Certificate.GetRenewalInfo(ctx, leaf)
	if err != nil || ri == nil || ri.ExtendedRenewalInfo == nil {
		return start, end, false
	}
	w := ri.SuggestedWindow
	if w.Start.IsZero() || w.End.IsZero() {
		return start, end, false
	}
	return w.Start, w.End, true
}

// alpnProvider answers tls-alpn-01 on the manager's listener.
type alpnProvider struct{ m *Manager }

func (p alpnProvider) Present(_ context.Context, domain, _ string, keyAuth string) error {
	cert, err := tlsalpn01.ChallengeCert(domain, keyAuth)
	if err != nil {
		return err
	}
	p.m.presentALPN(domain, cert)
	return nil
}

func (p alpnProvider) CleanUp(_ context.Context, domain, _ string, _ string) error {
	p.m.cleanALPN(domain)
	return nil
}

// httpProvider answers http-01 on the plain listener's challenge route.
type httpProvider struct{ m *Manager }

func (p httpProvider) Present(_ context.Context, _ string, token, keyAuth string) error {
	p.m.presentHTTP(token, keyAuth)
	return nil
}

func (p httpProvider) CleanUp(_ context.Context, _ string, token, _ string) error {
	p.m.cleanHTTP(token)
	return nil
}

// newDNSProvider builds one of the light dns-01 providers from settings
// under lego's variable names, without touching the process environment.
func newDNSProvider(name string, env map[string]string) (challenge.Provider, error) {
	get := func(k string) string { return env[k] }
	switch name {
	case "cloudflare":
		c := cloudflare.NewDefaultConfig()
		c.AuthEmail, c.AuthKey = get("CLOUDFLARE_EMAIL"), get("CLOUDFLARE_API_KEY")
		c.AuthToken, c.ZoneToken = get("CLOUDFLARE_DNS_API_TOKEN"), get("CLOUDFLARE_ZONE_API_TOKEN")
		if v := get("CLOUDFLARE_BASE_URL"); v != "" {
			c.BaseURL = v
		}
		if c.AuthToken == "" && c.AuthKey == "" {
			return nil, errors.New("cloudflare needs CLOUDFLARE_DNS_API_TOKEN (or CLOUDFLARE_EMAIL and CLOUDFLARE_API_KEY) in tls.acme.dnsEnv")
		}
		return cloudflare.NewDNSProviderConfig(c)
	case "exec":
		c := exec.NewDefaultConfig()
		c.Program, c.Mode = get("EXEC_PATH"), get("EXEC_MODE")
		if c.Program == "" {
			return nil, errors.New("exec needs EXEC_PATH in tls.acme.dnsEnv")
		}
		return exec.NewDNSProviderConfig(c)
	case "httpreq":
		c := httpreq.NewDefaultConfig()
		raw := get("HTTPREQ_ENDPOINT")
		if raw == "" {
			return nil, errors.New("httpreq needs HTTPREQ_ENDPOINT in tls.acme.dnsEnv")
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("HTTPREQ_ENDPOINT: %w", err)
		}
		c.Endpoint, c.Mode, c.Username, c.Password = u, get("HTTPREQ_MODE"), get("HTTPREQ_USERNAME"), get("HTTPREQ_PASSWORD")
		return httpreq.NewDNSProviderConfig(c)
	}
	return nil, fmt.Errorf("unknown dns provider %q (cloudflare, exec or httpreq)", name)
}
