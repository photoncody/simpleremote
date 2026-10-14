// Package auth signs techs in with OpenID Connect or the backup local admin,
// and guards the tech console API.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/httpx"
	"github.com/photoncody/simpleremote/server/internal/store"
)

// Identity is a signed-in tech.
type Identity struct {
	Subject string `json:"-"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Method  string `json:"method"` // "oidc" or "local"
}

// Label is a human-readable name for audit logs.
func (i Identity) Label() string {
	switch {
	case i.Email != "" && i.Name != "" && i.Name != i.Email:
		return i.Name + " <" + i.Email + ">"
	case i.Email != "":
		return i.Email
	default:
		return i.Name
	}
}

// Service handles login, logout, and request authentication.
type Service struct {
	cfg    *config.Config
	store  *store.Store
	log    *slog.Logger
	secure bool
	now    func() time.Time

	cookieName string
	origin     string

	oidcMu   sync.Mutex
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
	userinfo func(ctx context.Context, ts oauth2.TokenSource) (*oidc.UserInfo, error)

	ipLimiter     *httpx.Limiter
	globalLimiter *httpx.Limiter
	dummyHash     []byte
}

// New creates the auth service.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) *Service {
	secure := cfg.PublicURL.Scheme == "https"
	name := "sr_session"
	if secure {
		name = "__Host-sr_session"
	}
	dummy, _ := bcrypt.GenerateFromPassword([]byte("not-the-password"), bcryptCost)
	return &Service{
		cfg: cfg, store: st, log: log, secure: secure, now: time.Now,
		cookieName:    name,
		origin:        cfg.PublicURL.Scheme + "://" + cfg.PublicURL.Host,
		ipLimiter:     httpx.NewLimiter(time.Minute, 5),
		globalLimiter: httpx.NewLimiter(2*time.Second, 30),
		dummyHash:     dummy,
	}
}

// Routes registers the login endpoints.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/config", s.handleConfig)
	mux.HandleFunc("GET /auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET /auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("POST /api/auth/local", s.handleLocalLogin)
	mux.Handle("POST /api/auth/logout", s.Require(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/me", s.Require(http.HandlerFunc(s.handleMe)))
}

// Sweep removes expired logins and idle rate-limit buckets.
func (s *Service) Sweep(ctx context.Context) {
	if err := s.store.DeleteExpiredTechSessions(ctx, s.now()); err != nil {
		s.log.Warn("delete expired tech sessions", "err", err)
	}
	s.ipLimiter.Sweep()
	s.globalLimiter.Sweep()
}

func (s *Service) handleConfig(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, 200, map[string]any{
		"org_name":           s.cfg.OrgName,
		"oidc_enabled":       s.cfg.OIDC.Enabled(),
		"oidc_provider_name": s.cfg.OIDC.ProviderName,
		"auto_login":         s.cfg.OIDC.Enabled() && s.cfg.OIDC.AutoLogin,
		"local_enabled":      s.cfg.Admin.Enabled(),
	})
}

// ---- sessions --------------------------------------------------------------

type ctxKey struct{}

type authed struct {
	id   Identity
	sess store.TechSession
}

// FromContext returns the tech attached by Require.
func FromContext(ctx context.Context) (Identity, bool) {
	a, ok := ctx.Value(ctxKey{}).(authed)
	return a.id, ok
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Service) startSession(w http.ResponseWriter, r *http.Request, id Identity) error {
	tok := randToken()
	now := s.now()
	ts := store.TechSession{
		IDHash: hashToken(tok), Subject: id.Subject, Name: id.Name, Email: id.Email, Method: id.Method,
		CSRF: randToken(), CreatedAt: now, ExpiresAt: now.Add(s.cfg.TechSessionTTL),
	}
	if err := s.store.CreateTechSession(r.Context(), ts); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName, Value: tok, Path: "/", MaxAge: int(s.cfg.TechSessionTTL.Seconds()),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
	s.audit(r, id.Label(), "login", id.Method)
	return nil
}

// Lookup returns the tech signed in on this request, if any.
func (s *Service) Lookup(r *http.Request) (Identity, store.TechSession, bool) {
	c, err := r.Cookie(s.cookieName)
	if err != nil || c.Value == "" {
		return Identity{}, store.TechSession{}, false
	}
	ts, err := s.store.GetTechSession(r.Context(), hashToken(c.Value), s.now())
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("load tech session", "err", err)
		}
		return Identity{}, store.TechSession{}, false
	}
	return Identity{Subject: ts.Subject, Name: ts.Name, Email: ts.Email, Method: ts.Method}, ts, true
}

// Require rejects requests without a signed-in tech. Unsafe methods must also
// carry the session's CSRF token in X-CSRF-Token and come from our origin.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ts, ok := s.Lookup(r)
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "not signed in")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			tok := r.Header.Get("X-CSRF-Token")
			if !httpx.SameOrigin(r, s.origin) || subtle.ConstantTimeCompare([]byte(tok), []byte(ts.CSRF)) != 1 {
				httpx.Error(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, authed{id: id, sess: ts})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(ctxKey{}).(authed)
	httpx.WriteJSON(w, 200, map[string]any{
		"name": a.id.Name, "email": a.id.Email, "method": a.id.Method, "csrf_token": a.sess.CSRF,
		"expires_at": a.sess.ExpiresAt,
	})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	a := r.Context().Value(ctxKey{}).(authed)
	if err := s.store.DeleteTechSession(r.Context(), a.sess.IDHash); err != nil {
		httpx.Error(w, 500, "logout failed")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	s.audit(r, a.id.Label(), "logout", "")
	httpx.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// ---- local admin -----------------------------------------------------------

func (s *Service) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Admin.Enabled() {
		httpx.Error(w, http.StatusNotFound, "local login is disabled")
		return
	}
	if !httpx.SameOrigin(r, s.origin) {
		httpx.Error(w, http.StatusForbidden, "cross-origin request")
		return
	}
	ip := httpx.ClientIP(r)
	if !s.ipLimiter.Allow(ip) || !s.globalLimiter.Allow("local-login") {
		w.Header().Set("Retry-After", "60")
		httpx.Error(w, http.StatusTooManyRequests, "too many attempts, wait a minute and try again")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := httpx.ReadJSON(r, &body); err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}
	if !s.checkAdmin(body.Username, body.Password) {
		s.audit(r, body.Username, "login_failed", "local")
		httpx.Error(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	id := Identity{Subject: "local|" + s.cfg.Admin.Username, Name: s.cfg.Admin.Username, Method: "local"}
	if err := s.startSession(w, r, id); err != nil {
		s.log.Error("start session", "err", err)
		httpx.Error(w, 500, "login failed")
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Service) checkAdmin(user, pass string) bool {
	a := s.cfg.Admin
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.Username)) == 1
	var passOK bool
	if a.PasswordHash != "" {
		passOK = bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(pass)) == nil
	} else {
		// Hash both sides so the comparison time doesn't leak the length.
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(pass))
		x, y := sha256.Sum256([]byte(pass)), sha256.Sum256([]byte(a.Password))
		passOK = subtle.ConstantTimeCompare(x[:], y[:]) == 1
	}
	return userOK && passOK
}

// bcryptCost is lowered by tests.
var bcryptCost = 12

// HashPassword returns a bcrypt hash for ADMIN_PASSWORD_HASH.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

func (s *Service) audit(r *http.Request, actor, kind, detail string) {
	if err := s.store.AddEvent(r.Context(), store.Event{Actor: actor, Kind: kind, Detail: detail, IP: httpx.ClientIP(r)}); err != nil {
		s.log.Error("audit", "err", err)
	}
}

// SafeNext returns next if it is a local tech-console path, or "/tech".
func SafeNext(next string) string {
	if strings.HasPrefix(next, "/tech") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return "/tech"
}
