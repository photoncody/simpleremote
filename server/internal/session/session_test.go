package session

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/photoncody/simpleremote/internal/proto"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/httpx"
	"github.com/photoncody/simpleremote/server/internal/store"
)

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	m     *Manager
	store *store.Store
	jar   http.CookieJar
	tech  auth.Identity
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	pub, _ := url.Parse(srv.URL)
	cfg := &config.Config{
		PublicURL: pub, OrgName: "Acme IT", SessionSecret: []byte(strings.Repeat("k", 32)),
		TechSessionTTL: time.Hour, CodeTTL: 10 * time.Minute, SessionMaxDuration: time.Hour,
		Admin: config.Admin{Username: "admin", Password: "a-long-password"},
		ICE:   config.ICE{STUNURLs: []string{"stun:stun.example:3478"}},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := auth.New(cfg, st, log)
	m, err := New(context.Background(), cfg, st, log)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	mux.Handle("GET /ws/agent", m.AgentHandler(httpx.NewLimiter(time.Millisecond, 100)))
	mux.Handle("GET /ws/tech/{id}", m.TechHandler(a, srv.URL))
	srv.Config.Handler = httpx.RealIP(nil)(mux)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Post(srv.URL+"/api/auth/local", "application/json", strings.NewReader(`{"username":"admin","password":"a-long-password"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login: %v %v", err, resp)
	}
	resp.Body.Close()
	return &harness{t: t, srv: srv, m: m, store: st, jar: jar,
		tech: auth.Identity{Subject: "local|admin", Name: "admin", Method: "local"}}
}

type wsClient struct {
	t  *testing.T
	ws *websocket.Conn
}

func (h *harness) dialAgent() *wsClient {
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(h.srv.URL, "http")+"/ws/agent", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return &wsClient{t: h.t, ws: ws}
}

func (h *harness) dialTech(id string) *wsClient {
	hdr := http.Header{"Origin": {h.srv.URL}}
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(h.srv.URL, "http")+"/ws/tech/"+id,
		&websocket.DialOptions{HTTPHeader: hdr, HTTPClient: &http.Client{Jar: h.jar}})
	if err != nil {
		h.t.Fatal(err)
	}
	return &wsClient{t: h.t, ws: ws}
}

func (c *wsClient) send(typ string, v any) {
	b, _ := proto.Marshal(typ, v)
	if err := c.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) recv() proto.Envelope {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.ws.Read(ctx)
	if err != nil {
		c.t.Fatalf("recv: %v", err)
	}
	var env proto.Envelope
	_ = json.Unmarshal(b, &env)
	return env
}

// expect reads until a message of type typ arrives.
func (c *wsClient) expect(typ string, v any) {
	c.t.Helper()
	for i := 0; i < 10; i++ {
		env := c.recv()
		if env.Type == typ {
			if v != nil {
				_ = json.Unmarshal(env.Data, v)
			}
			return
		}
	}
	c.t.Fatalf("never got %s", typ)
}

func TestFullFlow(t *testing.T) {
	h := newHarness(t)
	ss, err := h.m.Create(context.Background(), h.tech, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.m.LookupCode(ss.Code); !ok {
		t.Fatal("new code not found")
	}

	agent := h.dialAgent()
	agent.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
	var hello proto.Hello
	agent.expect(proto.TypeHello, &hello)
	if hello.TechName != "admin" || hello.OrgName != "Acme IT" || hello.ResumeToken == "" || len(hello.ICEServers) != 1 {
		t.Fatalf("hello = %+v", hello)
	}
	if _, ok := h.m.LookupCode(ss.Code); ok {
		t.Fatal("code still valid after agent joined")
	}
	agent.send(proto.TypeAgentInfo, proto.AgentInfo{Hostname: "PC-1", OS: "windows", Arch: "amd64", Username: "bob"})

	// A second agent can't reuse the code.
	other := h.dialAgent()
	other.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
	other.expect(proto.TypeError, nil)

	tech := h.dialTech(ss.ID)
	tech.expect(proto.TypeHello, nil)
	var st proto.Status
	for st.Agent == nil {
		tech.expect(proto.TypeStatus, &st)
	}
	if st.State != proto.StateAwaitingConsent || st.Agent.Hostname != "PC-1" {
		t.Fatalf("status = %+v", st)
	}

	// Offers are not relayed before the user consents: the first offer the
	// agent sees must be the one sent after consent.
	tech.send(proto.TypeOffer, proto.SDP{Type: "offer", SDP: "early"})
	time.Sleep(50 * time.Millisecond)

	agent.send(proto.TypeConsent, proto.Consent{Allowed: true})
	tech.expect(proto.TypeStatus, &st)
	if st.State != proto.StateActive {
		t.Fatalf("after consent: %+v", st)
	}

	tech.send(proto.TypeOffer, proto.SDP{Type: "offer", SDP: "v=0"})
	var sdp proto.SDP
	agent.expect(proto.TypeOffer, &sdp)
	if sdp.SDP != "v=0" {
		t.Fatalf("offer = %+v", sdp)
	}
	agent.send(proto.TypeAnswer, proto.SDP{Type: "answer", SDP: "v=1"})
	tech.expect(proto.TypeAnswer, &sdp)

	// Agent reconnects with its resume token (e.g. after elevation).
	agent2 := h.dialAgent()
	agent2.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, ResumeToken: hello.ResumeToken})
	var hello2 proto.Hello
	agent2.expect(proto.TypeHello, &hello2)
	if !hello2.Resumed {
		t.Fatal("resumed hello should say Resumed")
	}

	tech.send(proto.TypeEnd, proto.End{})
	agent2.expect(proto.TypeEnd, nil)

	got, _ := h.store.GetSupportSession(context.Background(), ss.ID)
	if got.Live || got.State != proto.StateEnded || got.AgentHostname != "PC-1" || got.ConsentedAt.IsZero() {
		t.Fatalf("stored session = %+v", got)
	}
	ev, _ := h.store.ListEvents(context.Background(), store.EventFilter{SessionID: ss.ID})
	kinds := []string{}
	for _, e := range ev {
		kinds = append(kinds, e.Kind)
	}
	want := "created agent_connected tech_joined consent_granted agent_reconnected ended"
	if strings.Join(kinds, " ") != want {
		t.Fatalf("events = %v\nwant %s", kinds, want)
	}
}

// If the first connection drops before the agent receives its resume token,
// the agent can rejoin with the code. Once it acknowledges the token, the
// code is retired.
func TestCodeUsableUntilTokenAcked(t *testing.T) {
	h := newHarness(t)
	ss, _ := h.m.Create(context.Background(), h.tech, "")

	first := h.dialAgent()
	first.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
	// While an agent holds the code, nobody else can use it.
	other := h.dialAgent()
	other.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
	other.expect(proto.TypeError, nil)
	first.ws.CloseNow() // dropped before reading the hello

	var hello proto.Hello
	deadline := time.Now().Add(2 * time.Second)
	for {
		again := h.dialAgent()
		again.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
		env := again.recv()
		if env.Type == proto.TypeHello {
			_ = json.Unmarshal(env.Data, &hello)
			again.send(proto.TypeAgentInfo, proto.AgentInfo{Hostname: "PC"})
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rejoin with code failed: %s", env.Data)
		}
		time.Sleep(20 * time.Millisecond) // server hasn't noticed the drop yet
	}
	if hello.ResumeToken == "" {
		t.Fatal("no resume token on rejoin")
	}

	time.Sleep(50 * time.Millisecond)
	late := h.dialAgent()
	late.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
	late.expect(proto.TypeError, nil)
}

func TestConsentDenied(t *testing.T) {
	h := newHarness(t)
	ss, _ := h.m.Create(context.Background(), h.tech, "")
	tech := h.dialTech(ss.ID)
	tech.expect(proto.TypeHello, nil)
	agent := h.dialAgent()
	agent.send(proto.TypeJoin, proto.Join{ProtocolVersion: proto.Version, Code: ss.Code})
	agent.expect(proto.TypeHello, nil)
	agent.send(proto.TypeConsent, proto.Consent{Allowed: false})
	var end proto.End
	tech.expect(proto.TypeEnd, &end)
	if !strings.Contains(end.Reason, "declined") {
		t.Fatalf("end = %+v", end)
	}
}

func TestOtherTechCannotJoin(t *testing.T) {
	h := newHarness(t)
	ss, _ := h.m.Create(context.Background(), auth.Identity{Subject: "oidc|someone-else", Name: "Other"}, "")
	ws, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(h.srv.URL, "http")+"/ws/tech/"+ss.ID,
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {h.srv.URL}}, HTTPClient: &http.Client{Jar: h.jar}})
	if err == nil {
		ws.CloseNow()
		t.Fatal("expected dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("resp = %v", resp)
	}
}

func TestReapExpiresCode(t *testing.T) {
	h := newHarness(t)
	ss, _ := h.m.Create(context.Background(), h.tech, "")
	h.m.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	h.m.Reap()
	if _, ok := h.m.LookupCode(ss.Code); ok {
		t.Fatal("expired code still valid")
	}
	got, _ := h.store.GetSupportSession(context.Background(), ss.ID)
	if got.State != proto.StateExpired {
		t.Fatalf("state = %s", got.State)
	}
}
