package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base() map[string]string {
	return map[string]string{
		"PUBLIC_URL":          "https://support.example.com/",
		"OIDC_ISSUER":         "https://idp.example.com",
		"OIDC_CLIENT_ID":      "simpleremote",
		"OIDC_ALLOWED_GROUPS": "helpdesk, it-admins",
		"SESSION_SECRET":      strings.Repeat("s", 32),
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(mapEnv(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL.String() != "https://support.example.com" {
		t.Errorf("PublicURL = %s", c.PublicURL)
	}
	if !c.OIDC.Enabled() || !c.OIDC.AutoLogin {
		t.Error("OIDC should be enabled with auto-login by default")
	}
	if got := c.OIDC.AllowedGroups; len(got) != 2 || got[1] != "it-admins" {
		t.Errorf("AllowedGroups = %v", got)
	}
	if c.CodeTTL != 10*time.Minute || c.SessionMaxDuration != 4*time.Hour {
		t.Errorf("durations = %v %v", c.CodeTTL, c.SessionMaxDuration)
	}
	if c.ICE.TURNCredTTL != 4*time.Hour+15*time.Minute {
		t.Errorf("TURNCredTTL = %v", c.ICE.TURNCredTTL)
	}
}

func TestOIDCRequiresAllowList(t *testing.T) {
	env := base()
	delete(env, "OIDC_ALLOWED_GROUPS")
	if _, err := load(mapEnv(env)); err == nil || !strings.Contains(err.Error(), "no one is allowed") {
		t.Fatalf("expected allow-list error, got %v", err)
	}
	env["OIDC_ALLOW_ALL"] = "true"
	if _, err := load(mapEnv(env)); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsALoginMethod(t *testing.T) {
	env := map[string]string{"PUBLIC_URL": "https://x.example"}
	if _, err := load(mapEnv(env)); err == nil {
		t.Fatal("expected error without OIDC or admin")
	}
	env["ADMIN_USERNAME"] = "admin"
	env["ADMIN_PASSWORD_HASH"] = "$2a$10$abcdefghijklmnopqrstuuOJ4yk7cQb2mBzLqCoBf0ARBXbXyX4W6"
	c, err := load(mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Admin.Enabled() || c.OIDC.Enabled() {
		t.Error("expected admin only")
	}
}

func TestFileVariant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("from-file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := base()
	env["OIDC_CLIENT_SECRET_FILE"] = path
	c, err := load(mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.OIDC.ClientSecret != "from-file-secret" {
		t.Errorf("ClientSecret = %q", c.OIDC.ClientSecret)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"PUBLIC_URL":      "not a url",
		"CODE_TTL":        "5s",
		"TRUSTED_PROXIES": "nope",
		"TURN_URLS":       "turn:turn.example.com:3478",
		"STUN_URLS":       "http://stun",
		"LOG_LEVEL":       "loud",
		"SESSION_SECRET":  "short",
	}
	for k, v := range cases {
		env := base()
		env[k] = v
		if _, err := load(mapEnv(env)); err == nil {
			t.Errorf("%s=%q: expected error", k, v)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	env := base()
	env["TRUSTED_PROXIES"] = "10.0.0.0/8, 192.168.1.5"
	c, err := load(mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1].Bits() != 32 {
		t.Errorf("TrustedProxies = %v", c.TrustedProxies)
	}
}
