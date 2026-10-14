package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SupportSession is one remote-support session and its audit summary.
type SupportSession struct {
	ID            string    `json:"id"`
	Code          string    `json:"code"`
	State         string    `json:"state"`
	Live          bool      `json:"live"`
	TechSubject   string    `json:"-"`
	TechName      string    `json:"tech_name"`
	TechEmail     string    `json:"tech_email"`
	CreatedAt     time.Time `json:"created_at"`
	CodeExpiresAt time.Time `json:"code_expires_at"`
	ConnectedAt   time.Time `json:"connected_at"`
	ConsentedAt   time.Time `json:"consented_at"`
	EndedAt       time.Time `json:"ended_at"`
	EndReason     string    `json:"end_reason"`
	AgentHostname string    `json:"agent_hostname"`
	AgentOS       string    `json:"agent_os"`
	AgentArch     string    `json:"agent_arch"`
	AgentUser     string    `json:"agent_user"`
	AgentVersion  string    `json:"agent_version"`
	AgentIP       string    `json:"agent_ip"`
}

const supportCols = `id, code, state, live, tech_subject, tech_name, tech_email, created_at, code_expires_at,
	connected_at, consented_at, ended_at, end_reason, agent_hostname, agent_os, agent_arch, agent_user,
	agent_version, agent_ip`

type scanner interface{ Scan(...any) error }

func scanSupport(r scanner) (SupportSession, error) {
	var s SupportSession
	var created, codeExp int64
	var live int
	var connected, consented, ended sql.NullInt64
	err := r.Scan(&s.ID, &s.Code, &s.State, &live, &s.TechSubject, &s.TechName, &s.TechEmail, &created, &codeExp,
		&connected, &consented, &ended, &s.EndReason, &s.AgentHostname, &s.AgentOS, &s.AgentArch, &s.AgentUser,
		&s.AgentVersion, &s.AgentIP)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	s.Live = live == 1
	s.CreatedAt, s.CodeExpiresAt = time.UnixMilli(created), time.UnixMilli(codeExp)
	s.ConnectedAt, s.ConsentedAt, s.EndedAt = fromMS(connected), fromMS(consented), fromMS(ended)
	return s, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// InsertSupportSession creates a session. It returns ErrConflict if the code
// is already used by a live session.
func (s *Store) InsertSupportSession(ctx context.Context, ss SupportSession) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO support_sessions (`+supportCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ss.ID, ss.Code, ss.State, boolInt(ss.Live), ss.TechSubject, ss.TechName, ss.TechEmail,
		ms(ss.CreatedAt), ms(ss.CodeExpiresAt), nullMS(ss.ConnectedAt), nullMS(ss.ConsentedAt), nullMS(ss.EndedAt),
		ss.EndReason, ss.AgentHostname, ss.AgentOS, ss.AgentArch, ss.AgentUser, ss.AgentVersion, ss.AgentIP)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

// UpdateSupportSession overwrites every mutable field.
func (s *Store) UpdateSupportSession(ctx context.Context, ss SupportSession) error {
	res, err := s.db.ExecContext(ctx, `UPDATE support_sessions SET
		state = ?, live = ?, connected_at = ?, consented_at = ?, ended_at = ?, end_reason = ?,
		agent_hostname = ?, agent_os = ?, agent_arch = ?, agent_user = ?, agent_version = ?, agent_ip = ?
		WHERE id = ?`,
		ss.State, boolInt(ss.Live), nullMS(ss.ConnectedAt), nullMS(ss.ConsentedAt), nullMS(ss.EndedAt), ss.EndReason,
		ss.AgentHostname, ss.AgentOS, ss.AgentArch, ss.AgentUser, ss.AgentVersion, ss.AgentIP, ss.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSupportSession loads a session by ID.
func (s *Store) GetSupportSession(ctx context.Context, id string) (SupportSession, error) {
	return scanSupport(s.db.QueryRowContext(ctx, `SELECT `+supportCols+` FROM support_sessions WHERE id = ?`, id))
}

// GetLiveSessionByCode finds the live session using code.
func (s *Store) GetLiveSessionByCode(ctx context.Context, code string) (SupportSession, error) {
	return scanSupport(s.db.QueryRowContext(ctx, `SELECT `+supportCols+` FROM support_sessions WHERE code = ? AND live = 1`, code))
}

// ListFilter selects support sessions.
type ListFilter struct {
	LiveOnly bool
	Limit    int
	Offset   int
}

// ListSupportSessions returns sessions, newest first.
func (s *Store) ListSupportSessions(ctx context.Context, f ListFilter) ([]SupportSession, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	q := `SELECT ` + supportCols + ` FROM support_sessions`
	if f.LiveOnly {
		q += ` WHERE live = 1`
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, q, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SupportSession{}
	for rows.Next() {
		ss, err := scanSupport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// EndAllLive marks every live session as ended. It runs at startup, because
// live connections do not survive a server restart.
func (s *Store) EndAllLive(ctx context.Context, now time.Time, reason string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE support_sessions
		SET live = 0, ended_at = ?, end_reason = ?,
		    state = CASE WHEN state = 'pending' THEN 'expired' ELSE 'ended' END
		WHERE live = 1`, ms(now), reason)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Purge deletes finished sessions and events older than cutoff.
func (s *Store) Purge(ctx context.Context, cutoff time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE at < ?`, ms(cutoff)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM support_sessions WHERE live = 0 AND created_at < ?`, ms(cutoff)); err != nil {
		return err
	}
	return tx.Commit()
}
