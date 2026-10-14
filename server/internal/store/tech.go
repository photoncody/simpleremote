package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TechSession is a logged-in tech's browser session. Only a hash of the
// cookie value is stored.
type TechSession struct {
	IDHash    string
	Subject   string // OIDC "iss|sub", or "local|<username>"
	Name      string
	Email     string
	Method    string // "oidc" or "local"
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateTechSession stores a new login.
func (s *Store) CreateTechSession(ctx context.Context, t TechSession) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tech_sessions
		(id_hash, subject, name, email, method, csrf, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.IDHash, t.Subject, t.Name, t.Email, t.Method, t.CSRF, ms(t.CreatedAt), ms(t.ExpiresAt))
	return err
}

// GetTechSession returns an unexpired login.
func (s *Store) GetTechSession(ctx context.Context, idHash string, now time.Time) (TechSession, error) {
	var t TechSession
	var created, expires int64
	err := s.db.QueryRowContext(ctx, `SELECT id_hash, subject, name, email, method, csrf, created_at, expires_at
		FROM tech_sessions WHERE id_hash = ? AND expires_at > ?`, idHash, ms(now)).
		Scan(&t.IDHash, &t.Subject, &t.Name, &t.Email, &t.Method, &t.CSRF, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.CreatedAt, t.ExpiresAt = time.UnixMilli(created), time.UnixMilli(expires)
	return t, err
}

// DeleteTechSession logs a tech out.
func (s *Store) DeleteTechSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tech_sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteExpiredTechSessions removes stale logins.
func (s *Store) DeleteExpiredTechSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tech_sessions WHERE expires_at <= ?`, ms(now))
	return err
}
