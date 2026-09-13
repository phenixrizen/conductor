package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

// Clamp requested lifetimes to the database clock. Application clock skew must
// not make every login fail or allow a session to exceed its database lifetime.
func browserExpiry(ctx context.Context, tx pgx.Tx, expires time.Time, lifetime time.Duration) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read browser session clock: %w", err)
	}
	if !expires.After(now) {
		return time.Time{}, domain.ErrInvalidInput
	}
	if maximum := now.Add(lifetime); expires.After(maximum) {
		expires = maximum
	}
	return expires.UTC(), nil
}

func (p *Postgres) CreateBrowserLogin(ctx context.Context, login domain.BrowserLogin) error {
	if err := domain.ValidateBrowserLogin(login); err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin browser login: %w", err)
	}
	defer tx.Rollback(ctx)
	// Serializing admission keeps a burst of unauthenticated login attempts from
	// racing past the global bound. Consuming a login only decreases this count.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('conductor/browser-logins-capacity',0))`); err != nil {
		return fmt.Errorf("lock browser login capacity: %w", err)
	}
	if login.ExpiresAt, err = browserExpiry(ctx, tx, login.ExpiresAt, domain.BrowserLoginLifetime); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM browser_logins WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("prune expired browser logins: %w", err)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM browser_logins`).Scan(&count); err != nil {
		return fmt.Errorf("count browser logins: %w", err)
	}
	if count >= domain.MaxBrowserLogins {
		return domain.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO browser_logins(state_hash,binding_hash,nonce,code_verifier,expires_at) VALUES($1,$2,$3,$4,$5)`, login.StateHash, login.BindingHash, login.Nonce, login.CodeVerifier, login.ExpiresAt); err != nil {
		return fmt.Errorf("create browser login: %w", err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ConsumeBrowserLogin(ctx context.Context, stateHash, bindingHash string) (domain.BrowserLogin, error) {
	var login domain.BrowserLogin
	if !domain.ValidBrowserTokenHash(stateHash) || !domain.ValidBrowserTokenHash(bindingHash) {
		return login, domain.ErrUnauthenticated
	}
	// One DELETE is the replay boundary. A wrong browser binding does not consume
	// somebody else's login; concurrent valid callbacks can obtain it only once.
	err := p.pool.QueryRow(ctx, `DELETE FROM browser_logins WHERE state_hash=$1 AND binding_hash=$2 AND expires_at>now() RETURNING state_hash,binding_hash,nonce,code_verifier,expires_at`, stateHash, bindingHash).Scan(&login.StateHash, &login.BindingHash, &login.Nonce, &login.CodeVerifier, &login.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return login, domain.ErrUnauthenticated
	}
	if err != nil {
		return login, fmt.Errorf("consume browser login: %w", err)
	}
	login.ExpiresAt = login.ExpiresAt.UTC()
	return login, nil
}

func (p *Postgres) CreateBrowserSession(ctx context.Context, session domain.BrowserSession) error {
	if err := domain.ValidateBrowserSession(session); err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin browser session: %w", err)
	}
	defer tx.Rollback(ctx)
	principal, err := principalInTransaction(ctx, tx, session.Identity)
	if err != nil {
		return err
	}
	if principal.Kind != "human" {
		return domain.ErrUnauthenticated
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('conductor/browser-sessions-capacity',0))`); err != nil {
		return fmt.Errorf("lock browser session capacity: %w", err)
	}
	if session.ExpiresAt, err = browserExpiry(ctx, tx, session.ExpiresAt, domain.BrowserSessionLifetime); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM browser_sessions WHERE expires_at<=now()`); err != nil {
		return fmt.Errorf("prune expired browser sessions: %w", err)
	}
	var total, personal int
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE principal_id=$1) FROM browser_sessions`, principal.ID).Scan(&total, &personal); err != nil {
		return fmt.Errorf("count browser sessions: %w", err)
	}
	if total >= domain.MaxBrowserSessions || personal >= domain.MaxPrincipalBrowserSessions {
		return domain.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO browser_sessions(token_hash,csrf_token,principal_id,expires_at) VALUES($1,$2,$3,$4)`, session.TokenHash, session.CSRFToken, principal.ID, session.ExpiresAt); err != nil {
		return fmt.Errorf("create browser session: %w", err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) BrowserSession(ctx context.Context, tokenHash string) (domain.BrowserSession, error) {
	var session domain.BrowserSession
	if !domain.ValidBrowserTokenHash(tokenHash) {
		return session, domain.ErrUnauthenticated
	}
	err := p.pool.QueryRow(ctx, `SELECT s.token_hash,s.csrf_token,p.issuer,p.subject,s.expires_at FROM browser_sessions s JOIN access_principals p ON p.id=s.principal_id WHERE s.token_hash=$1 AND s.expires_at>now() AND p.active AND p.kind='human'`, tokenHash).Scan(&session.TokenHash, &session.CSRFToken, &session.Identity.Issuer, &session.Identity.Subject, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, domain.ErrUnauthenticated
	}
	if err != nil {
		return session, fmt.Errorf("read browser session: %w", err)
	}
	session.ExpiresAt = session.ExpiresAt.UTC()
	return session, nil
}

func (p *Postgres) RevokeBrowserSession(ctx context.Context, tokenHash string) error {
	// Logout is idempotent, including a malformed or already removed cookie. It
	// never changes another browser's session or the principal's review history.
	if !domain.ValidBrowserTokenHash(tokenHash) {
		return nil
	}
	if _, err := p.pool.Exec(ctx, `DELETE FROM browser_sessions WHERE token_hash=$1`, tokenHash); err != nil {
		return fmt.Errorf("revoke browser session: %w", err)
	}
	return nil
}
