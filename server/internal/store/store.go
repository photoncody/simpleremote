// Package store persists tech logins, support sessions, and audit events in
// SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique constraint fails.
var ErrConflict = errors.New("conflict")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "simpleremote.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	return open(dsn)
}

// OpenMemory opens an in-memory database for tests.
func OpenMemory() (*Store, error) {
	return open("file::memory:?_pragma=foreign_keys(1)")
}

func open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection keeps things simple and
	// is plenty for a support team.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE tech_sessions (
		id_hash    TEXT PRIMARY KEY,
		subject    TEXT NOT NULL,
		name       TEXT NOT NULL,
		email      TEXT NOT NULL,
		method     TEXT NOT NULL,
		csrf       TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE TABLE support_sessions (
		id             TEXT PRIMARY KEY,
		code           TEXT NOT NULL,
		state          TEXT NOT NULL,
		live           INTEGER NOT NULL DEFAULT 1,
		tech_subject   TEXT NOT NULL,
		tech_name      TEXT NOT NULL,
		tech_email     TEXT NOT NULL,
		created_at     INTEGER NOT NULL,
		code_expires_at INTEGER NOT NULL,
		connected_at   INTEGER,
		consented_at   INTEGER,
		ended_at       INTEGER,
		end_reason     TEXT NOT NULL DEFAULT '',
		agent_hostname TEXT NOT NULL DEFAULT '',
		agent_os       TEXT NOT NULL DEFAULT '',
		agent_arch     TEXT NOT NULL DEFAULT '',
		agent_user     TEXT NOT NULL DEFAULT '',
		agent_version  TEXT NOT NULL DEFAULT '',
		agent_ip       TEXT NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX support_sessions_live_code ON support_sessions(code) WHERE live = 1;
	CREATE INDEX support_sessions_created ON support_sessions(created_at);
	CREATE TABLE events (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		at         INTEGER NOT NULL,
		actor      TEXT NOT NULL,
		kind       TEXT NOT NULL,
		detail     TEXT NOT NULL DEFAULT '',
		ip         TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX events_session ON events(session_id, at);
	CREATE INDEX events_at ON events(at);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (v INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT v FROM schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version (v) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET v = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func nullMS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func fromMS(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64)
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
