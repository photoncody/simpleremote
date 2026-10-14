package store

import (
	"context"
	"database/sql"
	"time"
)

// Event is one audit log entry. SessionID is empty for events that are not
// tied to a support session, such as logins.
type Event struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
}

// AddEvent appends to the audit log.
func (s *Store) AddEvent(ctx context.Context, e Event) error {
	var sid any
	if e.SessionID != "" {
		sid = e.SessionID
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events (session_id, at, actor, kind, detail, ip) VALUES (?, ?, ?, ?, ?, ?)`,
		sid, ms(e.At), e.Actor, e.Kind, e.Detail, e.IP)
	return err
}

// EventFilter selects audit events.
type EventFilter struct {
	SessionID string // empty = all events
	Limit     int
	Offset    int
}

// ListEvents returns events, oldest first for a single session and newest
// first otherwise.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]Event, error) {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 500
	}
	var rows *sql.Rows
	var err error
	if f.SessionID != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT id, COALESCE(session_id, ''), at, actor, kind, detail, ip
			FROM events WHERE session_id = ? ORDER BY at, id LIMIT ? OFFSET ?`, f.SessionID, f.Limit, f.Offset)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id, COALESCE(session_id, ''), at, actor, kind, detail, ip
			FROM events ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`, f.Limit, f.Offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&e.ID, &e.SessionID, &at, &e.Actor, &e.Kind, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
