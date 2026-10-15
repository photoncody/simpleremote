package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const flowCookie = "sr_oidc"

// flowState is kept in a signed cookie between the redirect to the identity
// provider and the callback.
type flowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
	Expires  int64  `json:"e"`
}

func (s *Service) redirectURL() string {
	return s.cfg.PublicURL.String() + "/auth/oidc/callback"
}

// client discovers the provider on first use, so the server still starts
// (and the backup admin still works) when the identity provider is down.
func (s *Service) client(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oauth != nil {
		return s.oauth, s.verifier, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, s.cfg.OIDC.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("discover %s: %w", s.cfg.OIDC.Issuer, err)
	}
	s.oauth = &oauth2.Config{
		ClientID:     s.cfg.OIDC.ClientID,
		ClientSecret: s.cfg.OIDC.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  s.redirectURL(),
		Scopes:       s.cfg.OIDC.Scopes,
	}
	s.verifier = p.Verifier(&oidc.Config{ClientID: s.cfg.OIDC.ClientID})
	s.userinfo = func(ctx context.Context, ts oauth2.TokenSource) (*oidc.UserInfo, error) {
		return p.UserInfo(ctx, ts)
	}
	return s.oauth, s.verifier, nil
}

func (s *Service) signFlow(f flowState) string {
	js, _ := json.Marshal(f)
	payload := base64.RawURLEncoding.EncodeToString(js)
	mac := hmac.New(sha256.New, s.cfg.SessionSecret)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) readFlow(v string) (flowState, error) {
	var f flowState
	payload, sig, ok := strings.Cut(v, ".")
	if !ok {
		return f, errors.New("malformed")
	}
	mac := hmac.New(sha256.New, s.cfg.SessionSecret)
	mac.Write([]byte(payload))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return f, errors.New("bad signature")
	}
	js, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(js, &f); err != nil {
		return f, err
	}
	if s.now().Unix() > f.Expires {
		return f, errors.New("expired")
	}
	return f, nil
}

func (s *Service) loginError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/tech/login?error="+url.QueryEscape(code), http.StatusFound)
}

func (s *Service) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.OIDC.Enabled() {
		http.NotFound(w, r)
		return
	}
	oc, _, err := s.client(r.Context())
	if err != nil {
		s.log.Error("oidc discovery", "err", err)
		s.loginError(w, r, "provider_unavailable")
		return
	}
	f := flowState{
		State: randToken(), Nonce: randToken(), Verifier: oauth2.GenerateVerifier(),
		Next: SafeNext(r.URL.Query().Get("next")), Expires: s.now().Add(10 * time.Minute).Unix(),
	}
	http.SetCookie(w, &http.Cookie{
		Name: flowCookie, Value: s.signFlow(f), Path: "/auth/oidc/", MaxAge: 600,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, oc.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier)), http.StatusFound)
}

func (s *Service) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.OIDC.Enabled() {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.log.Warn("identity provider returned an error", "error", e, "description", q.Get("error_description"))
		s.loginError(w, r, "provider_error")
		return
	}
	c, err := r.Cookie(flowCookie)
	if err != nil {
		s.loginError(w, r, "session_expired")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: "", Path: "/auth/oidc/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
	f, err := s.readFlow(c.Value)
	if err != nil || q.Get("state") == "" || !hmac.Equal([]byte(q.Get("state")), []byte(f.State)) {
		s.loginError(w, r, "session_expired")
		return
	}
	oc, verifier, err := s.client(r.Context())
	if err != nil {
		s.loginError(w, r, "provider_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := oc.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		s.log.Warn("oidc code exchange", "err", err)
		s.loginError(w, r, "exchange_failed")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := verifier.Verify(ctx, raw)
	if err != nil || idt.Nonce != f.Nonce {
		s.log.Warn("oidc id token rejected", "err", err)
		s.loginError(w, r, "invalid_token")
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		s.loginError(w, r, "invalid_token")
		return
	}
	// Some providers only put groups in the userinfo response.
	if _, ok := claims[s.cfg.OIDC.GroupsClaim]; !ok && len(s.cfg.OIDC.AllowedGroups) > 0 && s.userinfo != nil {
		if ui, err := s.userinfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var extra map[string]any
			if ui.Claims(&extra) == nil {
				if g, ok := extra[s.cfg.OIDC.GroupsClaim]; ok {
					claims[s.cfg.OIDC.GroupsClaim] = g
				}
			}
		}
	}

	id := identityFromClaims(idt.Issuer, idt.Subject, claims)
	if ok, why := s.Authorize(claims); !ok {
		s.log.Info("oidc user not allowed", "user", id.Label(), "reason", why)
		s.audit(r, id.Label(), "login_denied", why)
		s.loginError(w, r, "not_allowed")
		return
	}
	if err := s.startSession(w, r, id); err != nil {
		s.log.Error("start session", "err", err)
		s.loginError(w, r, "server_error")
		return
	}
	http.Redirect(w, r, f.Next, http.StatusFound)
}

func identityFromClaims(iss, sub string, c map[string]any) Identity {
	str := func(k string) string { v, _ := c[k].(string); return v }
	id := Identity{Subject: iss + "|" + sub, Email: strings.ToLower(str("email")), Method: "oidc"}
	for _, k := range []string{"name", "preferred_username", "email"} {
		if v := str(k); v != "" {
			id.Name = v
			break
		}
	}
	if id.Name == "" {
		id.Name = sub
	}
	return id
}

// Authorize applies the OIDC_ALLOWED_* rules to a user's claims. Any one
// matching rule admits the user.
func (s *Service) Authorize(claims map[string]any) (bool, string) {
	o := s.cfg.OIDC
	if o.AllowAll {
		return true, ""
	}
	email, _ := claims["email"].(string)
	email = strings.ToLower(email)
	// Ignore the email when the provider explicitly says it isn't verified.
	if v, ok := claims["email_verified"].(bool); ok && !v {
		email = ""
	}
	if email != "" {
		if slices.Contains(o.AllowedEmails, email) {
			return true, ""
		}
		if _, domain, ok := strings.Cut(email, "@"); ok && slices.Contains(o.AllowedDomains, domain) {
			return true, ""
		}
	}
	if len(o.AllowedGroups) > 0 {
		for _, g := range groupsFrom(claims[o.GroupsClaim]) {
			for _, want := range o.AllowedGroups {
				// Keycloak sends full group paths like "/helpdesk".
				if g == want || strings.TrimPrefix(g, "/") == strings.TrimPrefix(want, "/") {
					return true, ""
				}
			}
		}
	}
	return false, "no matching group, email, or domain"
}

func groupsFrom(v any) []string {
	switch g := v.(type) {
	case []any:
		out := make([]string, 0, len(g))
		for _, x := range g {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return g
	case string:
		return strings.FieldsFunc(g, func(r rune) bool { return r == ',' || r == ' ' })
	}
	return nil
}
