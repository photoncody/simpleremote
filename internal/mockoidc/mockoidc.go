// Package mockoidc is a minimal OpenID Connect provider for tests and local
// development. It is never used in production builds of the server.
package mockoidc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// User is the identity the provider logs in as.
type User struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Groups        []string
}

// Provider is an in-memory OIDC provider.
type Provider struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// Interactive shows a form to choose the user instead of logging in
	// as DefaultUser immediately.
	Interactive bool

	mu          sync.Mutex
	DefaultUser User
	key         *rsa.PrivateKey
	signer      jose.Signer
	codes       map[string]grant
}

type grant struct {
	user      User
	nonce     string
	challenge string
	redirect  string
	expires   time.Time
}

// New creates a provider. Set Issuer to the URL it is served from.
func New(issuer, clientID, clientSecret string) (*Provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "mock"))
	if err != nil {
		return nil, err
	}
	return &Provider{
		Issuer: strings.TrimRight(issuer, "/"), ClientID: clientID, ClientSecret: clientSecret,
		DefaultUser: User{Subject: "tech-1", Email: "tech@example.com", EmailVerified: true, Name: "Test Tech", Groups: []string{"helpdesk"}},
		key:         key, signer: signer, codes: map[string]grant{},
	}, nil
}

// SetUser changes who logs in next.
func (p *Provider) SetUser(u User) {
	p.mu.Lock()
	p.DefaultUser = u
	p.mu.Unlock()
}

// ServeHTTP implements the provider endpoints.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		p.discovery(w)
	case "/authorize":
		p.authorize(w, r)
	case "/token":
		p.token(w, r)
	case "/jwks":
		p.jwks(w)
	case "/userinfo":
		p.userinfo(w, r)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Provider) discovery(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]any{
		"issuer":                                p.Issuer,
		"authorization_endpoint":                p.Issuer + "/authorize",
		"token_endpoint":                        p.Issuer + "/token",
		"jwks_uri":                              p.Issuer + "/jwks",
		"userinfo_endpoint":                     p.Issuer + "/userinfo",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
	})
}

var formTmpl = template.Must(template.New("f").Parse(`<!doctype html><title>Mock IdP</title>
<style>body{font-family:sans-serif;max-width:28rem;margin:3rem auto}label{display:block;margin:.6rem 0}input{width:100%}</style>
<h1>Mock identity provider</h1><p>Development only. Choose who to sign in as.</p>
<form method="post">{{range $k, $v := .Query}}<input type="hidden" name="{{$k}}" value="{{index $v 0}}">{{end}}
<label>Email <input name="email" value="{{.User.Email}}"></label>
<label>Name <input name="name" value="{{.User.Name}}"></label>
<label>Groups (comma separated) <input name="groups" value="{{.Groups}}"></label>
<button>Sign in</button></form>`))

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	q := r.Form
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" {
		http.Error(w, "bad client_id or response_type", 400)
		return
	}
	redirect := q.Get("redirect_uri")
	if _, err := url.ParseRequestURI(redirect); err != nil {
		http.Error(w, "bad redirect_uri", 400)
		return
	}
	p.mu.Lock()
	user := p.DefaultUser
	p.mu.Unlock()

	if p.Interactive {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = formTmpl.Execute(w, map[string]any{"Query": r.URL.Query(), "User": user, "Groups": strings.Join(user.Groups, ",")})
			return
		}
		user.Email = q.Get("email")
		user.Name = q.Get("name")
		user.Subject = "sub-" + user.Email
		user.Groups = nil
		for _, g := range strings.Split(q.Get("groups"), ",") {
			if g = strings.TrimSpace(g); g != "" {
				user.Groups = append(user.Groups, g)
			}
		}
	}

	code := randString()
	p.mu.Lock()
	p.codes[code] = grant{user: user, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: redirect,
		expires: time.Now().Add(time.Minute)}
	p.mu.Unlock()

	u, _ := url.Parse(redirect)
	v := u.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, ok := p.codes[code]
	delete(p.codes, code)
	p.mu.Unlock()
	if !ok || time.Now().After(g.expires) || r.PostForm.Get("redirect_uri") != g.redirect {
		writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	if g.challenge != "" {
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "PKCE mismatch"})
			return
		}
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.Issuer, "sub": g.user.Subject, "aud": p.ClientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"email": g.user.Email, "email_verified": g.user.EmailVerified, "name": g.user.Name,
		"groups": g.user.Groups,
	}
	if g.nonce != "" {
		claims["nonce"] = g.nonce
	}
	idToken, err := jwt.Signed(p.signer).Claims(claims).Serialize()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"access_token": "mock-" + randString(), "token_type": "Bearer", "expires_in": 300, "id_token": idToken,
	})
}

func (p *Provider) jwks(w http.ResponseWriter) {
	writeJSON(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "mock", Algorithm: "RS256", Use: "sig"}}})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer mock-") {
		writeJSON(w, 401, map[string]string{"error": "invalid_token"})
		return
	}
	p.mu.Lock()
	u := p.DefaultUser
	p.mu.Unlock()
	writeJSON(w, 200, map[string]any{"sub": u.Subject, "email": u.Email, "name": u.Name, "groups": u.Groups})
}

func randString() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
