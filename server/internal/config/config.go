// Package config loads server settings from environment variables.
//
// Every setting can also be read from a file by setting NAME_FILE to a path,
// which works with Docker and Kubernetes secrets.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/photoncody/simpleremote/internal/stamp"
)

// Config holds all server settings.
type Config struct {
	PublicURL      *url.URL
	ListenAddr     string
	TrustedProxies []netip.Prefix
	DataDir        string
	AgentDir       string
	SessionSecret  []byte
	OrgName        string
	LogLevel       slog.Level

	TechSessionTTL     time.Duration
	CodeTTL            time.Duration
	SessionMaxDuration time.Duration
	AuditRetentionDays int

	// RebootReconnect allows a tech to keep a live session connected across a
	// restart of the user's computer. RebootGrace is how long the server
	// holds the session open while the agent is gone during a restart.
	RebootReconnect bool
	RebootGrace     time.Duration

	OIDC  OIDC
	Admin Admin
	ICE   ICE

	// Warnings found while loading, logged at startup.
	Warnings []string
}

// OIDC configures single sign-on for techs.
type OIDC struct {
	Issuer         string
	ClientID       string
	ClientSecret   string
	Scopes         []string
	AutoLogin      bool
	ProviderName   string
	GroupsClaim    string
	AllowedGroups  []string
	AllowedEmails  []string
	AllowedDomains []string
	AllowAll       bool
}

// Enabled reports whether OIDC is configured.
func (o OIDC) Enabled() bool { return o.Issuer != "" }

// Admin configures the backup local administrator.
type Admin struct {
	Username     string
	PasswordHash string // bcrypt
	Password     string // plain text, discouraged
}

// Enabled reports whether the backup admin can log in.
func (a Admin) Enabled() bool {
	return a.Username != "" && (a.PasswordHash != "" || a.Password != "")
}

// ICE configures STUN and TURN servers handed to both peers.
type ICE struct {
	STUNURLs    []string
	TURNURLs    []string
	TURNSecret  string
	TURNCredTTL time.Duration
}

// Load reads the configuration from the environment.
func Load() (*Config, error) { return load(os.Getenv) }

func load(getenv func(string) string) (*Config, error) {
	e := &env{getenv: getenv}
	c := &Config{
		ListenAddr:         e.str("LISTEN_ADDR", ":8080"),
		DataDir:            e.str("DATA_DIR", "/data"),
		AgentDir:           e.str("AGENT_DIR", "/app/agents"),
		OrgName:            e.str("ORG_NAME", "SimpleRemote"),
		TechSessionTTL:     e.duration("TECH_SESSION_TTL", 12*time.Hour),
		CodeTTL:            e.duration("CODE_TTL", 10*time.Minute),
		SessionMaxDuration: e.duration("SESSION_MAX_DURATION", 4*time.Hour),
		AuditRetentionDays: e.int("AUDIT_RETENTION_DAYS", 365),
		RebootReconnect:    e.bool("REBOOT_RECONNECT", true),
		RebootGrace:        e.duration("REBOOT_GRACE", 10*time.Minute),
	}

	if raw := e.str("PUBLIC_URL", ""); raw == "" {
		e.fail("PUBLIC_URL is required (for example https://support.example.com)")
	} else if u, err := url.Parse(strings.TrimRight(raw, "/")); err != nil || stamp.ValidateServerURL(u.String()) != nil {
		e.fail("PUBLIC_URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	} else {
		c.PublicURL = u
	}

	for _, p := range e.list("TRUSTED_PROXIES") {
		if !strings.Contains(p, "/") {
			if a, err := netip.ParseAddr(p); err == nil {
				c.TrustedProxies = append(c.TrustedProxies, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
		}
		pfx, err := netip.ParsePrefix(p)
		if err != nil {
			e.fail("TRUSTED_PROXIES: invalid address or CIDR %q", p)
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, pfx.Masked())
	}

	switch strings.ToLower(e.str("LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		e.fail("LOG_LEVEL must be debug, info, warn, or error")
	}

	if s := e.str("SESSION_SECRET", ""); s != "" {
		if len(s) < 32 {
			e.fail("SESSION_SECRET must be at least 32 characters")
		}
		c.SessionSecret = []byte(s)
	} else {
		c.SessionSecret = make([]byte, 32)
		_, _ = rand.Read(c.SessionSecret)
		e.warn("SESSION_SECRET is not set; using a random value, so in-progress logins will fail after a restart")
	}

	if c.CodeTTL < time.Minute {
		e.fail("CODE_TTL must be at least 1m")
	}
	if c.SessionMaxDuration < 5*time.Minute {
		e.fail("SESSION_MAX_DURATION must be at least 5m")
	}
	if c.RebootGrace < time.Minute {
		e.fail("REBOOT_GRACE must be at least 1m")
	}
	if c.RebootGrace > c.SessionMaxDuration {
		c.RebootGrace = c.SessionMaxDuration
	}
	if c.TechSessionTTL < 5*time.Minute {
		e.fail("TECH_SESSION_TTL must be at least 5m")
	}
	if c.AuditRetentionDays < 0 {
		e.fail("AUDIT_RETENTION_DAYS must be 0 (keep forever) or more")
	}

	// OIDC
	o := &c.OIDC
	o.Issuer = strings.TrimRight(e.str("OIDC_ISSUER", ""), "/")
	o.ClientID = e.str("OIDC_CLIENT_ID", "")
	o.ClientSecret = e.str("OIDC_CLIENT_SECRET", "")
	o.Scopes = e.list("OIDC_SCOPES")
	if len(o.Scopes) == 0 {
		o.Scopes = []string{"openid", "profile", "email"}
	}
	o.AutoLogin = e.bool("OIDC_AUTO_LOGIN", true)
	o.ProviderName = e.str("OIDC_PROVIDER_NAME", "Single sign-on")
	o.GroupsClaim = e.str("OIDC_GROUPS_CLAIM", "groups")
	o.AllowedGroups = e.list("OIDC_ALLOWED_GROUPS")
	o.AllowedEmails = lower(e.list("OIDC_ALLOWED_EMAILS"))
	o.AllowedDomains = lower(e.list("OIDC_ALLOWED_DOMAINS"))
	o.AllowAll = e.bool("OIDC_ALLOW_ALL", false)
	for i, d := range o.AllowedDomains {
		o.AllowedDomains[i] = strings.TrimPrefix(d, "@")
	}
	if o.Enabled() {
		if o.ClientID == "" {
			e.fail("OIDC_CLIENT_ID is required when OIDC_ISSUER is set")
		}
		if !strings.HasPrefix(o.Issuer, "https://") && !strings.HasPrefix(o.Issuer, "http://") {
			e.fail("OIDC_ISSUER must be an http(s) URL")
		}
		if !o.AllowAll && len(o.AllowedGroups)+len(o.AllowedEmails)+len(o.AllowedDomains) == 0 {
			e.fail("OIDC is enabled but no one is allowed in: set OIDC_ALLOWED_GROUPS, OIDC_ALLOWED_EMAILS, or OIDC_ALLOWED_DOMAINS (or OIDC_ALLOW_ALL=true to admit every user your provider authenticates)")
		}
		if o.AllowAll {
			e.warn("OIDC_ALLOW_ALL=true: every user who can sign in to your identity provider can use SimpleRemote")
		}
	}

	// Backup admin
	a := &c.Admin
	a.Username = e.str("ADMIN_USERNAME", "")
	a.PasswordHash = e.str("ADMIN_PASSWORD_HASH", "")
	a.Password = e.str("ADMIN_PASSWORD", "")
	if a.Username != "" && a.PasswordHash == "" && a.Password == "" {
		e.fail("ADMIN_USERNAME is set but neither ADMIN_PASSWORD_HASH nor ADMIN_PASSWORD is")
	}
	if a.Username == "" && (a.PasswordHash != "" || a.Password != "") {
		e.fail("ADMIN_PASSWORD_HASH/ADMIN_PASSWORD is set but ADMIN_USERNAME is not")
	}
	if a.PasswordHash != "" && !strings.HasPrefix(a.PasswordHash, "$2") {
		e.fail("ADMIN_PASSWORD_HASH must be a bcrypt hash (generate one with: simpleremote-server hash-password)")
	}
	if a.PasswordHash == "" && a.Password != "" {
		if len(a.Password) < 12 {
			e.fail("ADMIN_PASSWORD must be at least 12 characters")
		}
		e.warn("ADMIN_PASSWORD is set in plain text; prefer ADMIN_PASSWORD_HASH")
	}

	if !o.Enabled() && !a.Enabled() {
		e.fail("no way to log in: configure OIDC_ISSUER/OIDC_CLIENT_ID and/or ADMIN_USERNAME/ADMIN_PASSWORD_HASH")
	}

	// ICE
	c.ICE.STUNURLs = e.list("STUN_URLS")
	c.ICE.TURNURLs = e.list("TURN_URLS")
	c.ICE.TURNSecret = e.str("TURN_SECRET", "")
	c.ICE.TURNCredTTL = e.duration("TURN_CRED_TTL", c.SessionMaxDuration+15*time.Minute)
	if len(c.ICE.TURNURLs) > 0 && c.ICE.TURNSecret == "" {
		e.fail("TURN_URLS is set but TURN_SECRET is not")
	}
	for _, u := range append(append([]string{}, c.ICE.STUNURLs...), c.ICE.TURNURLs...) {
		if !strings.HasPrefix(u, "stun:") && !strings.HasPrefix(u, "stuns:") && !strings.HasPrefix(u, "turn:") && !strings.HasPrefix(u, "turns:") {
			e.fail("invalid ICE server URL %q (expected stun:, stuns:, turn:, or turns:)", u)
		}
	}
	if len(c.ICE.TURNURLs) == 0 {
		e.warn("TURN_URLS is not set; connections between strict NATs or firewalls will fail")
	}

	if len(e.errs) > 0 {
		return nil, errors.Join(e.errs...)
	}
	c.Warnings = e.warnings
	return c, nil
}

type env struct {
	getenv   func(string) string
	errs     []error
	warnings []string
}

func (e *env) fail(format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf(format, args...))
}

func (e *env) warn(format string, args ...any) {
	e.warnings = append(e.warnings, fmt.Sprintf(format, args...))
}

func (e *env) str(name, def string) string {
	if path := e.getenv(name + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			e.fail("%s_FILE: %v", name, err)
			return def
		}
		return strings.TrimSpace(string(b))
	}
	if v := strings.TrimSpace(e.getenv(name)); v != "" {
		return v
	}
	return def
}

func (e *env) list(name string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(e.str(name, ""), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (e *env) bool(name string, def bool) bool {
	v := e.str(name, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.fail("%s must be true or false, got %q", name, v)
		return def
	}
	return b
}

func (e *env) int(name string, def int) int {
	v := e.str(name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.fail("%s must be a whole number, got %q", name, v)
		return def
	}
	return n
}

func (e *env) duration(name string, def time.Duration) time.Duration {
	v := e.str(name, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.fail("%s must be a duration like 10m or 4h, got %q", name, v)
		return def
	}
	return d
}

func lower(in []string) []string {
	for i, s := range in {
		in[i] = strings.ToLower(s)
	}
	return in
}
