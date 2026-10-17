package api

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
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/photoncody/simpleremote/internal/stamp"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/dist"
	"github.com/photoncody/simpleremote/server/internal/session"
	"github.com/photoncody/simpleremote/server/internal/store"
)

func newServer(t *testing.T) (*httptest.Server, *session.Manager) {
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
		OIDC:  config.OIDC{Issuer: "https://idp.invalid", ClientID: "x", AutoLogin: true, AllowAll: true},
		Admin: config.Admin{Username: "admin", Password: "a-long-password"},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	agents := t.TempDir()
	_ = os.WriteFile(filepath.Join(agents, "simpleremote-agent-windows-amd64.exe"), []byte("MZ"), 0o755)
	m, _ := session.New(context.Background(), cfg, st, log)
	s := &Server{Config: cfg, Store: st, Auth: auth.New(cfg, st, log), Sessions: m, Dist: dist.New(agents), Log: log,
		Web: fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "favicon.svg": {Data: []byte("<svg/>")}}}
	srv.Config.Handler = s.Handler()
	return srv, m
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestPublicCodeAndDownload(t *testing.T) {
	srv, m := newServer(t)
	ss, _ := m.Create(context.Background(), auth.Identity{Subject: "s", Name: "Tech"}, "")

	resp, _ := http.Post(srv.URL+"/api/public/code", "application/json", strings.NewReader(`{"code":"000000"}`))
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("bad code = %d", resp.StatusCode)
	}

	resp, _ = http.Post(srv.URL+"/api/public/code", "application/json", strings.NewReader(`{"code":" `+ss.Code+` "}`))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "windows-amd64") || !strings.Contains(string(body), `"tech_name":"Tech"`) {
		t.Fatalf("good code = %d %s", resp.StatusCode, body)
	}

	resp, _ = http.Get(srv.URL + "/d/" + ss.Code + "/windows-amd64")
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "SimpleRemote-"+ss.Code+".exe") {
		t.Fatalf("download = %d %v", resp.StatusCode, resp.Header)
	}
	got, err := stamp.Parse(data)
	if err != nil || got.Code != ss.Code || got.ServerURL != srv.URL {
		t.Fatalf("stamp = %+v %v", got, err)
	}

	resp, _ = http.Get(srv.URL + "/d/" + ss.Code + "/linux-amd64")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("missing platform = %d", resp.StatusCode)
	}
}

func TestSPARouting(t *testing.T) {
	srv, _ := newServer(t)
	c := noRedirect()

	resp, _ := c.Get(srv.URL + "/tech/session/abc")
	resp.Body.Close()
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/oidc/login?next=%2Ftech%2Fsession%2Fabc") {
		t.Fatalf("tech page = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, p := range []string{"/", "/tech/login", "/tech/login/local", "/some/client/route"} {
		resp, _ = c.Get(srv.URL + p)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(b) != "<html>app</html>" {
			t.Errorf("%s = %d %q", p, resp.StatusCode, b)
		}
		if resp.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s missing CSP", p)
		}
	}
	resp, _ = c.Get(srv.URL + "/favicon.svg")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "<svg/>" {
		t.Errorf("static file = %q", b)
	}
	resp, _ = c.Get(srv.URL + "/api/sessions")
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("unauthenticated API = %d", resp.StatusCode)
	}
	resp, _ = c.Get(srv.URL + "/api/nope")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("unknown API = %d", resp.StatusCode)
	}
}

func TestCSVSafe(t *testing.T) {
	got := csvSafe([]string{"=cmd()", "PC-1", "@x", ""})
	if got[0] != "'=cmd()" || got[1] != "PC-1" || got[2] != "'@x" || got[3] != "" {
		t.Fatalf("csvSafe = %v", got)
	}
}

func TestReusableAgentRoutes(t *testing.T) {
	srv, m := newServer(t)
	for _, path := range []string{"/api/agents", "/api/agents/windows-amd64"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("unauthorized %s = %d", path, resp.StatusCode)
		}
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Post(srv.URL+"/api/auth/local", "application/json", strings.NewReader(`{"username":"admin","password":"a-long-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	resp, err = c.Get(srv.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		ServerURL string          `json:"server_url"`
		Platforms []dist.Platform `json:"platforms"`
	}
	err = json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || info.ServerURL != srv.URL || len(info.Platforms) != 1 {
		t.Fatalf("list = %+v, %v", info, err)
	}
	// Query parameters cannot supply a session code or override server config.
	resp, err = c.Get(srv.URL + "/api/agents/windows-amd64?code=123456&resume=secret&server=https://other.example.com")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	want := stamp.Stamp{ServerURL: srv.URL, OrgName: "Acme IT"}
	got, err := stamp.Parse(data)
	if resp.StatusCode != 200 || err != nil || got != want {
		t.Fatalf("download = %d %+v %v", resp.StatusCode, got, err)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(resp.Header.Get("Content-Disposition"), "SimpleRemote-Technician.exe") {
		t.Fatal("download headers")
	}
	// A second request uses the prepared artifact and has an independent
	// streaming reader, including standard HTTP range handling.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/agents/windows-amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-1")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusPartialContent || string(prefix) != "MZ" || resp.ContentLength != 2 {
		t.Fatalf("range download = %d %q %v", resp.StatusCode, prefix, err)
	}
	if len(m.List(auth.Identity{Subject: "local|admin"})) != 0 {
		t.Fatal("download created a session")
	}
	for _, platform := range []string{"linux-amd64", "unknown"} {
		resp, err = c.Get(srv.URL + "/api/agents/" + platform)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("missing %s = %d", platform, resp.StatusCode)
		}
	}
}
