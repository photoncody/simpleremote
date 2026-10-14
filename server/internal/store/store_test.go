package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTechSessions(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	now := time.Now()
	ts := TechSession{IDHash: "h1", Subject: "local|admin", Name: "Admin", Method: "local", CSRF: "c",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateTechSession(ctx, ts); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTechSession(ctx, "h1", now)
	if err != nil || got.Subject != "local|admin" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := s.GetTechSession(ctx, "h1", now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	_ = s.DeleteTechSession(ctx, "h1")
	if _, err := s.GetTechSession(ctx, "h1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}

func TestSupportSessions(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	now := time.Now().Truncate(time.Millisecond)
	ss := SupportSession{ID: "s1", Code: "123456789", State: "pending", Live: true, TechSubject: "x", TechName: "Tech",
		CreatedAt: now, CodeExpiresAt: now.Add(10 * time.Minute)}
	if err := s.InsertSupportSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	dup := ss
	dup.ID = "s2"
	if err := s.InsertSupportSession(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate live code: %v", err)
	}

	got, err := s.GetLiveSessionByCode(ctx, "123456789")
	if err != nil || got.ID != "s1" || !got.CreatedAt.Equal(now) || !got.ConnectedAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}

	got.State, got.Live, got.EndedAt, got.EndReason = "ended", false, now, "tech ended"
	if err := s.UpdateSupportSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLiveSessionByCode(ctx, "123456789"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ended session still live: %v", err)
	}
	// The code can be reused once the old session is no longer live.
	if err := s.InsertSupportSession(ctx, dup); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListSupportSessions(ctx, ListFilter{LiveOnly: true})
	if err != nil || len(list) != 1 || list[0].ID != "s2" {
		t.Fatalf("live list = %+v, %v", list, err)
	}
	n, err := s.EndAllLive(ctx, now, "server restarted")
	if err != nil || n != 1 {
		t.Fatalf("EndAllLive = %d, %v", n, err)
	}
	got, _ = s.GetSupportSession(ctx, "s2")
	if got.State != "expired" || got.Live {
		t.Fatalf("pending session after restart = %+v", got)
	}
}

func TestEventsAndPurge(t *testing.T) {
	s, ctx := newStore(t), context.Background()
	old := time.Now().Add(-48 * time.Hour)
	_ = s.InsertSupportSession(ctx, SupportSession{ID: "old", Code: "111111111", State: "ended", CreatedAt: old, CodeExpiresAt: old})
	_ = s.AddEvent(ctx, Event{SessionID: "old", At: old, Actor: "tech", Kind: "created"})
	_ = s.AddEvent(ctx, Event{Actor: "admin", Kind: "login"})

	ev, err := s.ListEvents(ctx, EventFilter{SessionID: "old"})
	if err != nil || len(ev) != 1 || ev[0].Kind != "created" {
		t.Fatalf("events = %+v, %v", ev, err)
	}
	if err := s.Purge(ctx, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListEvents(ctx, EventFilter{})
	if len(all) != 1 || all[0].Kind != "login" {
		t.Fatalf("after purge = %+v", all)
	}
	if _, err := s.GetSupportSession(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old session not purged")
	}
}
