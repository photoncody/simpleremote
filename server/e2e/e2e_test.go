//go:build e2e

// Package e2e runs a full session: the real server (in process), a real agent
// binary on an X display, and a Go WebRTC client standing in for the tech's
// browser.
//
//	E2E_AGENT_BIN=/path/to/agent-built-with-tags-e2e DISPLAY=:99 go test -tags e2e ./server/e2e/
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/photoncody/simpleremote/internal/proto"
	"github.com/photoncody/simpleremote/server/internal/api"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/dist"
	"github.com/photoncody/simpleremote/server/internal/session"
	"github.com/photoncody/simpleremote/server/internal/store"
)

func TestFullSession(t *testing.T) {
	agentBin := os.Getenv("E2E_AGENT_BIN")
	if agentBin == "" || os.Getenv("DISPLAY") == "" {
		t.Skip("set E2E_AGENT_BIN and DISPLAY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// ---- server ----
	agentDir := t.TempDir()
	data, err := os.ReadFile(agentBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "simpleremote-agent-linux-amd64"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(nil)
	defer srv.Close()
	pub, _ := url.Parse(srv.URL)
	cfg := &config.Config{
		PublicURL: pub, OrgName: "E2E Org", SessionSecret: []byte(strings.Repeat("e", 32)),
		TechSessionTTL: time.Hour, CodeTTL: 10 * time.Minute, SessionMaxDuration: time.Hour,
		Admin: config.Admin{Username: "admin", Password: "e2e-password-123"},
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	authSvc := auth.New(cfg, st, log)
	mgr, err := session.New(ctx, cfg, st, log)
	if err != nil {
		t.Fatal(err)
	}
	s := &api.Server{Config: cfg, Store: st, Auth: authSvc, Sessions: mgr, Dist: dist.New(agentDir), Log: log,
		Web: fstest.MapFS{"index.html": {Data: []byte("app")}}}
	srv.Config.Handler = s.Handler()

	// ---- tech signs in and creates a session ----
	ok := func(resp *http.Response, err error) *http.Response { return mustOK(t, resp, err) }
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	ok(hc.Post(srv.URL+"/api/auth/local", "application/json", strings.NewReader(`{"username":"admin","password":"e2e-password-123"}`)))
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	decode(t, ok(hc.Get(srv.URL+"/api/me")), &me)
	req, _ := http.NewRequest("POST", srv.URL+"/api/sessions", nil)
	req.Header.Set("X-CSRF-Token", me.CSRF)
	var created struct{ ID, Code string }
	decode(t, ok(hc.Do(req)), &created)
	t.Logf("session %s code %s", created.ID, created.Code)

	// ---- end user downloads and runs the stamped agent ----
	resp := ok(http.Get(srv.URL + "/d/" + created.Code + "/linux-amd64"))
	exe := filepath.Join(t.TempDir(), "SimpleRemote-"+created.Code)
	out, _ := os.Create(exe)
	_, _ = io.Copy(out, resp.Body)
	resp.Body.Close()
	out.Close()
	_ = os.Chmod(exe, 0o755)
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "SIMPLEREMOTE_E2E_AUTO_ALLOW=1")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// ---- tech connects to signaling and waits for consent ----
	wsu := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/tech/" + created.ID
	ws, _, err := websocket.Dial(ctx, wsu, &websocket.DialOptions{HTTPClient: hc, HTTPHeader: http.Header{"Origin": {srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	sig := make(chan proto.Envelope, 64)
	go func() {
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				close(sig)
				return
			}
			var env proto.Envelope
			_ = json.Unmarshal(b, &env)
			sig <- env
		}
	}()
	send := func(typ string, v any) {
		b, _ := proto.Marshal(typ, v)
		if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}

	var hello proto.Hello
	waitFor(t, ctx, sig, func(env proto.Envelope) bool {
		switch env.Type {
		case proto.TypeHello:
			_ = json.Unmarshal(env.Data, &hello)
		case proto.TypeStatus:
			var st proto.Status
			_ = json.Unmarshal(env.Data, &st)
			return st.State == proto.StateActive && st.AgentConnected
		}
		return false
	}, "session to become active")

	// ---- tech opens WebRTC like the browser does ----
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	frames := make(chan uint32, 16)
	tiles := 0
	ordered := true
	video, _ := pc.CreateDataChannel(proto.ChannelVideo, &webrtc.DataChannelInit{Ordered: &ordered})
	control, _ := pc.CreateDataChannel(proto.ChannelControl, &webrtc.DataChannelInit{Ordered: &ordered})
	input, _ := pc.CreateDataChannel(proto.ChannelInput, &webrtc.DataChannelInit{Ordered: &ordered})
	displays := make(chan []proto.Display, 4)
	control.OnMessage(func(m webrtc.DataChannelMessage) {
		var c proto.Control
		_ = json.Unmarshal(m.Data, &c)
		if c.T == proto.CtlDisplays {
			displays <- c.Displays
		}
	})
	video.OnMessage(func(m webrtc.DataChannelMessage) {
		if len(m.Data) > 0 && m.Data[0] == proto.VideoTile {
			tiles++
		}
		if len(m.Data) == 5 && m.Data[0] == proto.VideoFrameEnd {
			seq := uint32(m.Data[1])<<24 | uint32(m.Data[2])<<16 | uint32(m.Data[3])<<8 | uint32(m.Data[4])
			b, _ := json.Marshal(proto.Control{T: proto.CtlAck, Seq: seq})
			_ = control.SendText(string(b))
			select {
			case frames <- seq:
			default:
			}
		}
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			j := c.ToJSON()
			send(proto.TypeICE, proto.ICECandidate{Candidate: j.Candidate, SDPMid: j.SDPMid, SDPMLineIndex: j.SDPMLineIndex})
		}
	})
	offer, _ := pc.CreateOffer(nil)
	_ = pc.SetLocalDescription(offer)
	send(proto.TypeOffer, proto.SDP{Type: "offer", SDP: offer.SDP})

	go func() {
		for env := range sig {
			switch env.Type {
			case proto.TypeAnswer:
				var sdp proto.SDP
				_ = json.Unmarshal(env.Data, &sdp)
				_ = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp.SDP})
			case proto.TypeICE:
				var c proto.ICECandidate
				_ = json.Unmarshal(env.Data, &c)
				_ = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex})
			}
		}
	}()

	select {
	case seq := <-frames:
		t.Logf("first frame %d with %d tiles", seq, tiles)
	case <-ctx.Done():
		t.Fatal("no video frame received")
	}
	var disp []proto.Display
	select {
	case disp = <-displays:
	case <-ctx.Done():
		t.Fatal("no display list")
	}
	if len(disp) == 0 {
		t.Fatal("agent reported no displays")
	}

	// ---- input reaches the desktop ----
	b, _ := json.Marshal(proto.Input{T: proto.InputMouseMove, X: 0.25, Y: 0.5})
	if err := input.SendText(string(b)); err != nil {
		t.Fatal(err)
	}
	wantX := int(0.25*float64(disp[0].Width-1) + 0.5)
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := exec.Command("xdotool", "getmouselocation", "--shell").Output()
		if err == nil && strings.Contains(string(out), "X="+strconv.Itoa(wantX)+"\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pointer did not move to x=%d: %s %v", wantX, out, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// ---- tech ends the session; the audit log has the whole story ----
	send(proto.TypeEnd, proto.End{})
	time.Sleep(500 * time.Millisecond)
	events, err := st.ListEvents(ctx, store.EventFilter{SessionID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"created", "downloaded", "agent_connected", "consent_granted", "tech_joined", "ended"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit log missing %q: %s", want, joined)
		}
	}
	ss, _ := st.GetSupportSession(ctx, created.ID)
	if ss.AgentHostname == "" || ss.State != proto.StateEnded {
		t.Errorf("session row = %+v", ss)
	}
}

func mustOK(t *testing.T, resp *http.Response, err error) *http.Response {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: %d %s", resp.Request.Method, resp.Request.URL, resp.StatusCode, b)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, ctx context.Context, ch <-chan proto.Envelope, done func(proto.Envelope) bool, what string) {
	t.Helper()
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				t.Fatalf("signaling closed while waiting for %s", what)
			}
			if done(env) {
				return
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
