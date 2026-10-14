package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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

	resp, _ := http.Post(srv.URL+"/api/public/code", "application/json", strings.NewReader(`{"code":"000-000-000"}`))
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("bad code = %d", resp.StatusCode)
	}

	resp, _ = http.Post(srv.URL+"/api/public/code", "application/json", strings.NewReader(`{"code":"`+stamp.FormatCode(ss.Code)+`"}`))
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
