package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/photoncody/simpleremote/agent/internal/platform"
	"github.com/photoncody/simpleremote/internal/proto"
)

type fakeUI struct {
	asks   atomic.Int32
	ended  chan string
	active chan struct{}
}

func (f *fakeUI) Connecting(string)                    {}
func (f *fakeUI) AskConsent(proto.Hello)               { f.asks.Add(1) }
func (f *fakeUI) NeedPermissions(platform.Permissions) {}
func (f *fakeUI) Active(proto.Hello)                   { f.active <- struct{}{} }
func (f *fakeUI) Reconnecting()                        {}
func (f *fakeUI) Chat(string, string)                  {}
func (f *fakeUI) Activity(string)                      {}
func (f *fakeUI) ElevationRequested(string)            {}
func (f *fakeUI) Ended(r string)                       { f.ended <- r }

// A signaling reconnect while the consent prompt is open must not start a
// second waiter: a stale one would later time out and end the session.
func TestReconnectKeepsOneConsentWaiter(t *testing.T) {
	var mu sync.Mutex
	var conns []*websocket.Conn
	consents := make(chan proto.Consent, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns = append(conns, ws)
		mu.Unlock()
		ctx := context.Background()
		_, _, _ = ws.Read(ctx) // join
		b, _ := proto.Marshal(proto.TypeHello, proto.Hello{SessionID: "s", TechName: "Tech", ResumeToken: "tok"})
		_ = ws.Write(ctx, websocket.MessageText, b)
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var env proto.Envelope
			_ = json.Unmarshal(data, &env)
			if env.Type == proto.TypeConsent {
				var c proto.Consent
				_ = json.Unmarshal(env.Data, &c)
				consents <- c
			}
		}
	}))
	defer srv.Close()

	ui := &fakeUI{ended: make(chan string, 1), active: make(chan struct{}, 4)}
	s := New(Options{ServerURL: srv.URL, Code: "123456", UI: ui, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	go s.Run()
	defer s.finish("test over", false)

	waitFor(t, func() bool { return ui.asks.Load() == 1 })
	// Drop the connection; the agent reconnects and gets another hello.
	mu.Lock()
	conns[0].CloseNow()
	mu.Unlock()
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(conns) == 2 })
	time.Sleep(200 * time.Millisecond)
	if n := ui.asks.Load(); n != 1 {
		t.Fatalf("consent prompt shown %d times, want 1", n)
	}

	s.Respond(true)
	select {
	case c := <-consents:
		if !c.Allowed {
			t.Fatal("consent not allowed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consent never sent")
	}
	<-ui.active
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// If the server never got the user's Allow (the connection dropped while it
// was being sent), the agent resends it after reconnecting.
func TestConsentResentAfterReconnect(t *testing.T) {
	var n atomic.Int32
	consents := make(chan int32, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn := n.Add(1)
		ctx := context.Background()
		_, _, _ = ws.Read(ctx) // join
		// Resumed stays false: the server hasn't recorded consent.
		b, _ := proto.Marshal(proto.TypeHello, proto.Hello{SessionID: "s", TechName: "Tech", ResumeToken: "tok"})
		_ = ws.Write(ctx, websocket.MessageText, b)
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var env proto.Envelope
			_ = json.Unmarshal(data, &env)
			if env.Type == proto.TypeConsent {
				if conn == 1 {
					ws.CloseNow() // lost before the server processed it
					return
				}
				consents <- conn
			}
		}
	}))
	defer srv.Close()

	ui := &fakeUI{ended: make(chan string, 1), active: make(chan struct{}, 4)}
	s := New(Options{ServerURL: srv.URL, Code: "123456", UI: ui, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	go s.Run()
	defer s.finish("test over", false)

	waitFor(t, func() bool { return ui.asks.Load() == 1 })
	s.Respond(true)
	select {
	case conn := <-consents:
		if conn != 2 {
			t.Fatalf("consent recorded on connection %d", conn)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consent was not resent after reconnecting")
	}
}
