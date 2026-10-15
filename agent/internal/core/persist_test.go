package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/photoncody/simpleremote/agent/internal/persist"
	"github.com/photoncody/simpleremote/agent/internal/platform"
	"github.com/photoncody/simpleremote/internal/proto"
)

// isolatePersist points the per-user config dir at a temp dir, so these tests
// never touch the real autostart locations.
func isolatePersist(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("avoids writing to the real HKCU Run key")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
}

// persistServer is a signaling server that answers join with hello and
// records what the agent sends back.
type persistServer struct {
	*httptest.Server
	hello   proto.Hello
	persist chan proto.Persist
}

func newPersistServer(t *testing.T, hello proto.Hello) *persistServer {
	t.Helper()
	ps := &persistServer{hello: hello, persist: make(chan proto.Persist, 4)}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		_, _, _ = ws.Read(ctx) // join
		b, _ := proto.Marshal(proto.TypeHello, ps.hello)
		_ = ws.Write(ctx, websocket.MessageText, b)
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var env proto.Envelope
			_ = json.Unmarshal(data, &env)
			if env.Type == proto.TypePersist {
				var p proto.Persist
				_ = json.Unmarshal(env.Data, &p)
				ps.persist <- p
			}
		}
	}))
	t.Cleanup(ps.Close)
	return ps
}

func newPersistSession(t *testing.T, ps *persistServer, opts Options) (*Session, *fakeUI) {
	t.Helper()
	ui := &fakeUI{ended: make(chan string, 2), active: make(chan struct{}, 4)}
	opts.ServerURL = ps.URL
	opts.ResumeToken = "tok"
	opts.UI = ui
	opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(opts)
	go s.Run()
	t.Cleanup(func() { s.finish("test over", false) })
	return s, ui
}

func waitPersist(t *testing.T, ch <-chan proto.Persist) proto.Persist {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("agent never reported its reboot-reconnect state")
		return proto.Persist{}
	}
}

// Turning it on in a resumed session sets it up, and the saved file records
// the user who allowed the session.
func TestPersistArmsOnResume(t *testing.T) {
	isolatePersist(t)
	until := time.Now().Add(time.Hour)
	ps := newPersistServer(t, proto.Hello{SessionID: "s", TechName: "Tech", Resumed: true,
		ResumeToken: "tok", Persist: true, PersistUntil: until.Unix()})
	_, ui := newPersistSession(t, ps, Options{})

	<-ui.active
	waitFor(t, func() bool { return persist.Armed() })
	info, _ := persist.Load()
	if info.ResumeToken != "tok" || info.Owner != platform.Username() {
		t.Fatalf("saved session = %+v", info)
	}
	if info.Expires.Unix() != until.Unix() {
		t.Fatalf("expiry = %v, want %v", info.Expires, until)
	}
}

// A process that did not set it up itself must still clear it when the tech
// turns it off — the case after a restart, where a previous process set it up.
func TestPersistOffClearsAnotherProcessArrangement(t *testing.T) {
	isolatePersist(t)
	// Stand in for what the pre-restart process left behind.
	if err := persist.Arm(persist.Info{ServerURL: "https://x", ResumeToken: "tok",
		Owner: platform.Username(), Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	ps := newPersistServer(t, proto.Hello{SessionID: "s", TechName: "Tech", Resumed: true, ResumeToken: "tok"})
	s, ui := newPersistSession(t, ps, Options{})
	<-ui.active

	s.applyPersist(false, time.Time{})
	if persist.Armed() {
		t.Fatal("reconnect-after-restart left in place after the tech turned it off")
	}
}

// Running as a different account than the user who allowed the session must
// not set anything up, and must tell the server so the toggle goes back off.
func TestPersistRefusedForAnotherUser(t *testing.T) {
	isolatePersist(t)
	ps := newPersistServer(t, proto.Hello{SessionID: "s", TechName: "Tech", Resumed: true, ResumeToken: "tok"})
	s, ui := newPersistSession(t, ps, Options{Owner: "someone-else"})
	<-ui.active

	s.applyPersist(true, time.Now().Add(time.Hour))
	if persist.Armed() {
		t.Fatal("set up reconnect in an account that never allowed the session")
	}
	if p := waitPersist(t, ps.persist); p.On {
		t.Fatal("agent reported it as on")
	}
}

// Ending the session clears the arrangement.
func TestPersistClearedWhenSessionEnds(t *testing.T) {
	isolatePersist(t)
	ps := newPersistServer(t, proto.Hello{SessionID: "s", TechName: "Tech", Resumed: true,
		ResumeToken: "tok", Persist: true, PersistUntil: time.Now().Add(time.Hour).Unix()})
	s, ui := newPersistSession(t, ps, Options{})
	<-ui.active
	waitFor(t, func() bool { return persist.Armed() })

	s.finish("ended by the user", true)
	if persist.Armed() {
		t.Fatal("reconnect-after-restart outlived the session")
	}
}

// A session whose server is unreachable at login keeps trying rather than
// giving up on the first failure.
func TestAutostartRetriesAtLogin(t *testing.T) {
	isolatePersist(t)
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	// A server that refuses the first dial, then accepts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-mu:
			w.WriteHeader(http.StatusServiceUnavailable) // "network not ready"
			return
		default:
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		_, _, _ = ws.Read(ctx)
		b, _ := proto.Marshal(proto.TypeHello, proto.Hello{SessionID: "s", TechName: "Tech",
			Resumed: true, ResumeToken: "tok"})
		_ = ws.Write(ctx, websocket.MessageText, b)
		select {}
	}))
	defer srv.Close()

	ui := &fakeUI{ended: make(chan string, 2), active: make(chan struct{}, 4)}
	s := New(Options{ServerURL: srv.URL, ResumeToken: "tok", UI: ui, FromAutostart: true,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	go s.Run()
	defer s.finish("test over", false)

	select {
	case <-ui.active:
	case reason := <-ui.ended:
		t.Fatalf("gave up at login instead of retrying: %s", reason)
	case <-time.After(10 * time.Second):
		t.Fatal("never resumed the session")
	}
}
