package auth

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

	"github.com/photoncody/simpleremote/internal/mockoidc"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/httpx"
	"github.com/photoncody/simpleremote/server/internal/store"
)

func init() { bcryptCost = 4 }

type harness struct {
	srv   *httptest.Server
	idp   *mockoidc.Provider
	svc   *Service
	store *store.Store
}

func newHarness(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	idpSrv := httptest.NewServer(nil)
	t.Cleanup(idpSrv.Close)
	idp, err := mockoidc.New(idpSrv.URL, "client", "secret")
	if err != nil {
		t.Fatal(err)
	}
	idpSrv.Config.Handler = idp

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	h := &harness{idp: idp, store: st}
	h.srv = httptest.NewServer(nil)
	t.Cleanup(h.srv.Close)
	pub, _ := url.Parse(h.srv.URL)
	hash, _ := HashPassword("correct horse battery")
	cfg := &config.Config{
		PublicURL: pub, OrgName: "Test", SessionSecret: []byte(strings.Repeat("k", 32)),
		TechSessionTTL: time.Hour,
		OIDC: config.OIDC{Issuer: idpSrv.URL, ClientID: "client", ClientSecret: "secret",
			Scopes: []string{"openid", "email"}, AutoLogin: true, GroupsClaim: "groups", AllowedGroups: []string{"helpdesk"}},
		Admin: config.Admin{Username: "admin", PasswordHash: hash},
	}
	if mutate != nil {
		mutate(cfg)
	}
	h.svc = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	h.svc.Routes(mux)
	mux.Handle("POST /api/protected", h.svc.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := FromContext(r.Context())
		httpx.WriteJSON(w, 200, id)
	})))
	h.srv.Config.Handler = httpx.RealIP(nil)(mux)
	return h
}

func client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func getJSON(t *testing.T, c *http.Client, u string, v any) int {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil {
		_ = json.NewDecoder(resp.Body).Decode(v)
	}
	return resp.StatusCode
}

func TestOIDCLoginFlow(t *testing.T) {
	h := newHarness(t, nil)
	c := client(t)

	resp, err := c.Get(h.srv.URL + "/auth/oidc/login?next=/tech/session/abc")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/tech/session/abc" {
		t.Fatalf("ended at %s, want /tech/session/abc", resp.Request.URL)
	}

	var me struct {
		Name, Email, Method string
		CSRF                string `json:"csrf_token"`
	}
	if code := getJSON(t, c, h.srv.URL+"/api/me", &me); code != 200 {
		t.Fatalf("/api/me = %d", code)
	}
	if me.Email != "tech@example.com" || me.Method != "oidc" || me.CSRF == "" {
		t.Fatalf("me = %+v", me)
	}

	// Unsafe requests need the CSRF token.
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/protected", nil)
	resp, _ = c.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("POST without CSRF = %d", resp.StatusCode)
	}
	req.Header.Set("X-CSRF-Token", me.CSRF)
	resp, _ = c.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST with CSRF = %d", resp.StatusCode)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, _ = c.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-origin POST = %d", resp.StatusCode)
	}

	ev, _ := h.store.ListEvents(context.Background(), store.EventFilter{})
	if len(ev) != 1 || ev[0].Kind != "login" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestOIDCDeniedUser(t *testing.T) {
	h := newHarness(t, nil)
	h.idp.SetUser(mockoidc.User{Subject: "x", Email: "someone@else.com", EmailVerified: true, Groups: []string{"sales"}})
	c := client(t)
	resp, err := c.Get(h.srv.URL + "/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/tech/login" || resp.Request.URL.Query().Get("error") != "not_allowed" {
		t.Fatalf("ended at %s", resp.Request.URL)
	}
	if code := getJSON(t, c, h.srv.URL+"/api/me", nil); code != 401 {
		t.Fatalf("/api/me = %d", code)
	}
}

func TestOIDCForgedState(t *testing.T) {
	h := newHarness(t, nil)
	c := client(t)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := c.Get(h.srv.URL + "/auth/oidc/callback?code=x&state=y")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "error=session_expired") {
		t.Fatalf("Location = %s", loc)
	}
}

func TestLocalLogin(t *testing.T) {
	h := newHarness(t, nil)
	c := client(t)
	post := func(user, pass string) int {
		body := `{"username":"` + user + `","password":"` + pass + `"}`
		resp, err := c.Post(h.srv.URL+"/api/auth/local", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("admin", "wrong"); code != 401 {
		t.Fatalf("wrong password = %d", code)
	}
	if code := post("admin", "correct horse battery"); code != 200 {
		t.Fatalf("right password = %d", code)
	}
	var me struct{ Name, Method string }
	if code := getJSON(t, c, h.srv.URL+"/api/me", &me); code != 200 || me.Method != "local" {
		t.Fatalf("/api/me = %d %+v", code, me)
	}
	for i := 0; i < 10; i++ {
		post("admin", "wrong")
	}
	if code := post("admin", "correct horse battery"); code != 429 {
		t.Fatalf("expected rate limit, got %d", code)
	}
}

func TestAuthorizeRules(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.OIDC.AllowedEmails = []string{"boss@corp.com"}
		c.OIDC.AllowedDomains = []string{"it.corp.com"}
		c.OIDC.AllowedGroups = []string{"helpdesk"}
	})
	cases := []struct {
		claims map[string]any
		want   bool
	}{
		{map[string]any{"email": "Boss@Corp.com"}, true},
		{map[string]any{"email": "a@it.corp.com"}, true},
		{map[string]any{"email": "a@it.corp.com", "email_verified": false}, false},
		{map[string]any{"groups": []any{"/helpdesk"}}, true},
		{map[string]any{"groups": "sales helpdesk"}, true},
		{map[string]any{"email": "a@evil.com", "groups": []any{"sales"}}, false},
	}
	for _, c := range cases {
		if got, _ := h.svc.Authorize(c.claims); got != c.want {
			t.Errorf("Authorize(%v) = %v, want %v", c.claims, got, c.want)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/tech/session/1":      "/tech/session/1",
		"//evil.com/tech":      "/tech",
		"https://evil.com":     "/tech",
		"/techx\r\nSet-Cookie": "/tech",
		"":                     "/tech",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q", in, got)
		}
	}
}
