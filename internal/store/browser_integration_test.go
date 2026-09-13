package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func browserHash(label string) string {
	digest := sha256.Sum256([]byte("synthetic browser fixture: " + label))
	return hex.EncodeToString(digest[:])
}
func browserSecret(label string) string {
	digest := sha256.Sum256([]byte("synthetic secret fixture: " + label))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
func browserLogin(label string) domain.BrowserLogin {
	return domain.BrowserLogin{StateHash: browserHash(label + " state"), BindingHash: browserHash(label + " browser"), Nonce: browserSecret(label + " nonce"), CodeVerifier: browserSecret(label + " verifier"), ExpiresAt: time.Now().UTC().Add(4 * time.Minute).Truncate(time.Microsecond)}
}
func browserSession(label string) domain.BrowserSession {
	return domain.BrowserSession{TokenHash: browserHash(label + " session"), CSRFToken: browserSecret(label + " csrf"), Identity: domain.AccessIdentity{Issuer: "https://identity.example.test", Subject: "author"}, ExpiresAt: time.Now().UTC().Add(50 * time.Minute).Truncate(time.Microsecond)}
}

func TestBrowserLoginBindingReplayAndConcurrentConsumption(t *testing.T) {
	t.Parallel()
	ctx, p, reopen := integrationStore(t)
	login := browserLogin("one use")
	if err := p.CreateBrowserLogin(ctx, login); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ConsumeBrowserLogin(ctx, login.StateHash, browserHash("wrong browser")); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("wrong browser consumed attempt: %v", err)
	}
	p.Close()
	p = reopen()
	const contenders = 8
	type result struct {
		login domain.BrowserLogin
		err   error
	}
	results := make(chan result, contenders)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < contenders; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			value, err := p.ConsumeBrowserLogin(ctx, login.StateHash, login.BindingHash)
			results <- result{value, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			if !reflect.DeepEqual(result.login, login) {
				t.Fatalf("stored exchange material changed: %+v", result.login)
			}
		} else if !errors.Is(result.err, domain.ErrUnauthenticated) {
			t.Fatalf("consume failed unexpectedly: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("authorization attempt consumed %d times", successes)
	}
	if _, err := p.ConsumeBrowserLogin(ctx, login.StateHash, login.BindingHash); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("callback replay accepted: %v", err)
	}
	var remaining int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM browser_logins`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("consumed exchange material retained: count=%d err=%v", remaining, err)
	}
}

func TestBrowserLoginExpiryAndAdmissionBound(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	login := browserLogin("bounded admission")
	for _, expiry := range []time.Time{time.Now().Add(-time.Second)} {
		invalid := login
		invalid.ExpiresAt = expiry
		if err := p.CreateBrowserLogin(ctx, invalid); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid login lifetime accepted: %v", err)
		}
	}
	if _, err := p.pool.Exec(ctx, `INSERT INTO browser_logins(state_hash,binding_hash,nonce,code_verifier,expires_at) SELECT lpad(to_hex(n),64,'0'),$1,$2,$3,now()+interval '4 minutes' FROM generate_series(1,$4) n`, login.BindingHash, login.Nonce, login.CodeVerifier, domain.MaxBrowserLogins); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateBrowserLogin(ctx, login); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("global login bound exceeded: %v", err)
	}
	expiredHash := fmt.Sprintf("%064x", 1)
	if _, err := p.pool.Exec(ctx, `UPDATE browser_logins SET created_at=now()-interval '6 minutes',expires_at=now()-interval '1 minute' WHERE state_hash=$1`, expiredHash); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ConsumeBrowserLogin(ctx, expiredHash, login.BindingHash); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expired login consumed: %v", err)
	}
	if err := p.CreateBrowserLogin(ctx, login); err != nil {
		t.Fatalf("expired login was not pruned for admission: %v", err)
	}
	var count int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM browser_logins`).Scan(&count); err != nil || count != domain.MaxBrowserLogins {
		t.Fatalf("bounded login count=%d err=%v", count, err)
	}
	if _, err := p.ConsumeBrowserLogin(ctx, login.StateHash, login.BindingHash); err != nil {
		t.Fatalf("new login not retained: %v", err)
	}
}

func TestBrowserSessionHumanEligibilityDurabilityAndRevocation(t *testing.T) {
	t.Parallel()
	ctx, p, reopen := integrationStore(t)
	provisionAccess(t, ctx, p)
	session := browserSession("human session")
	if err := p.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = reopen()
	stored, err := p.BrowserSession(ctx, session.TokenHash)
	if err != nil || !reflect.DeepEqual(stored, session) {
		t.Fatalf("session did not survive pool reopen: %+v %v", stored, err)
	}
	for _, subject := range []string{"worker", "unregistered"} {
		rejected := browserSession(subject)
		rejected.Identity.Subject = subject
		if err := p.CreateBrowserSession(ctx, rejected); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("%s obtained browser session: %v", subject, err)
		}
	}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "human-author", Issuer: session.Identity.Issuer, Subject: "author", Kind: "human", Active: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BrowserSession(ctx, session.TokenHash); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("disabled principal session accepted: %v", err)
	}
	if err := p.CreateBrowserSession(ctx, browserSession("inactive")); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("inactive principal session created: %v", err)
	}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "human-author", Issuer: session.Identity.Issuer, Subject: "author", Kind: "human", Active: true}}}); err != nil {
		t.Fatal(err)
	}
	second := browserSession("another independent browser")
	if err := p.CreateBrowserSession(ctx, second); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := p.RevokeBrowserSession(ctx, session.TokenHash); err != nil {
			t.Fatalf("idempotent logout failed: %v", err)
		}
	}
	if _, err := p.BrowserSession(ctx, session.TokenHash); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("revoked browser session accepted: %v", err)
	}
	if _, err := p.BrowserSession(ctx, second.TokenHash); err != nil {
		t.Fatalf("logout revoked another browser: %v", err)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE browser_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE token_hash=$1`, second.TokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BrowserSession(ctx, second.TokenHash); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expired session accepted: %v", err)
	}
	for _, expiry := range []time.Time{time.Now().Add(-time.Second)} {
		invalid := browserSession("invalid expiry")
		invalid.ExpiresAt = expiry
		if err := p.CreateBrowserSession(ctx, invalid); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("invalid session lifetime accepted: %v", err)
		}
	}
}

func TestBrowserSessionCapacityIsSerializedWithoutEviction(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	contenders := domain.MaxPrincipalBrowserSessions + 1
	results := make(chan error, contenders)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < contenders; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			results <- p.CreateBrowserSession(ctx, browserSession(fmt.Sprint(index)))
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, rejected := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, domain.ErrUnavailable) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if successes != domain.MaxPrincipalBrowserSessions || rejected != 1 {
		t.Fatalf("concurrent session admission: accepted=%d denied=%d", successes, rejected)
	}
	var count int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM browser_sessions WHERE principal_id='human-author'`).Scan(&count); err != nil || count != domain.MaxPrincipalBrowserSessions {
		t.Fatalf("session eviction or capacity error: count=%d err=%v", count, err)
	}
	// One expired record releases one slot. Active sessions remain untouched.
	if _, err := p.pool.Exec(ctx, `UPDATE browser_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE token_hash=(SELECT token_hash FROM browser_sessions ORDER BY token_hash LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateBrowserSession(ctx, browserSession("expired slot")); err != nil {
		t.Fatalf("expired session did not release a slot: %v", err)
	}
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM browser_sessions`).Scan(&count); err != nil || count != domain.MaxPrincipalBrowserSessions {
		t.Fatalf("unexpected retained session count=%d err=%v", count, err)
	}
}

func TestBrowserSessionGlobalCapacity(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	// A bounded bulk fixture represents 500 principals with 20 sessions each, so
	// the target principal has room individually while the global store is full.
	if _, err := p.pool.Exec(ctx, `INSERT INTO access_principals(id,issuer,subject,kind,active) SELECT 'capacity-'||n,'https://capacity.example.test','subject-'||n,'human',true FROM generate_series(1,$1) n`, domain.MaxBrowserSessions/domain.MaxPrincipalBrowserSessions); err != nil {
		t.Fatal(err)
	}
	session := browserSession("global admission")
	if _, err := p.pool.Exec(ctx, `INSERT INTO browser_sessions(token_hash,csrf_token,principal_id,expires_at) SELECT lpad(to_hex(n),64,'0'),$1,'capacity-'||(((n-1)/$2)+1),now()+interval '50 minutes' FROM generate_series(1,$3) n`, session.CSRFToken, domain.MaxPrincipalBrowserSessions, domain.MaxBrowserSessions); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateBrowserSession(ctx, session); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("global browser session capacity exceeded: %v", err)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE browser_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE token_hash=$1`, fmt.Sprintf("%064x", 1)); err != nil {
		t.Fatal(err)
	}
	if err := p.CreateBrowserSession(ctx, session); err != nil {
		t.Fatalf("global expired slot not reused: %v", err)
	}
	if _, err := p.BrowserSession(ctx, session.TokenHash); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedBrowserCredentialsDoNotReachStorage(t *testing.T) {
	// An unconnected store proves malformed hash inputs are rejected before I/O.
	p := &Postgres{}
	ctx := context.Background()
	if _, err := p.ConsumeBrowserLogin(ctx, "raw-cookie", browserHash("binding")); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal(err)
	}
	if _, err := p.BrowserSession(ctx, "raw-cookie"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal(err)
	}
	if err := p.RevokeBrowserSession(ctx, "raw-cookie"); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserLifetimeClampsApplicationClockSkew(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	login := browserLogin("ahead application clock")
	login.ExpiresAt = time.Now().Add(time.Hour)
	if err := p.CreateBrowserLogin(ctx, login); err != nil {
		t.Fatal(err)
	}
	consumed, err := p.ConsumeBrowserLogin(ctx, login.StateHash, login.BindingHash)
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := p.pool.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	if consumed.ExpiresAt.After(now.Add(domain.BrowserLoginLifetime)) || !consumed.ExpiresAt.Before(login.ExpiresAt) {
		t.Fatalf("login lifetime was not clamped: %s", consumed.ExpiresAt)
	}
	session := browserSession("ahead application clock")
	session.ExpiresAt = time.Now().Add(3 * time.Hour)
	if err := p.CreateBrowserSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	stored, err := p.BrowserSession(ctx, session.TokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.pool.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	if stored.ExpiresAt.After(now.Add(domain.BrowserSessionLifetime)) || !stored.ExpiresAt.Before(session.ExpiresAt) {
		t.Fatalf("session lifetime was not clamped: %s", stored.ExpiresAt)
	}
}
