// Package api wires the server's HTTP routes together.
package api

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/photoncody/simpleremote/internal/stamp"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/dist"
	"github.com/photoncody/simpleremote/server/internal/httpx"
	"github.com/photoncody/simpleremote/server/internal/session"
	"github.com/photoncody/simpleremote/server/internal/store"
)

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	Config   *config.Config
	Store    *store.Store
	Auth     *auth.Service
	Sessions *session.Manager
	Dist     *dist.Dist
	Log      *slog.Logger
	// Web is the built single-page app (index.html plus assets).
	Web fs.FS

	codeLimiter  *httpx.Limiter
	agentLimiter *httpx.Limiter
}

// Handler builds the root handler.
func (s *Server) Handler() http.Handler {
	// End users get a few tries at typing their code; guessing is throttled.
	s.codeLimiter = httpx.NewLimiter(6*time.Second, 10)
	s.agentLimiter = httpx.NewLimiter(6*time.Second, 10)

	origin := s.Config.PublicURL.Scheme + "://" + s.Config.PublicURL.Host
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	s.Auth.Routes(mux)

	// Public (end user) endpoints.
	mux.HandleFunc("GET /api/public/config", s.publicConfig)
	mux.Handle("POST /api/public/code", s.codeLimiter.Middleware(http.HandlerFunc(s.checkCode)))
	mux.Handle("GET /d/{code}/{platform}", s.codeLimiter.Middleware(http.HandlerFunc(s.download)))
	mux.Handle("GET /ws/agent", s.Sessions.AgentHandler(s.agentLimiter))

	// Tech console.
	req := s.Auth.Require
	mux.Handle("GET /api/agents", req(http.HandlerFunc(s.agentDownloads)))
	mux.Handle("GET /api/agents/{platform}", req(http.HandlerFunc(s.downloadReusable)))
	mux.Handle("GET /api/sessions", req(http.HandlerFunc(s.listSessions)))
	mux.Handle("POST /api/sessions", req(http.HandlerFunc(s.createSession)))
	mux.Handle("GET /api/sessions/{id}", req(http.HandlerFunc(s.getSession)))
	mux.Handle("DELETE /api/sessions/{id}", req(http.HandlerFunc(s.endSession)))
	mux.Handle("GET /api/audit/sessions", req(http.HandlerFunc(s.auditSessions)))
	mux.Handle("GET /api/audit/sessions.csv", req(http.HandlerFunc(s.auditCSV)))
	mux.Handle("GET /api/audit/sessions/{id}/events", req(http.HandlerFunc(s.auditSessionEvents)))
	mux.Handle("GET /api/audit/events", req(http.HandlerFunc(s.auditEvents)))
	mux.Handle("GET /ws/tech/{id}", s.Sessions.TechHandler(s.Auth, origin))

	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, http.StatusNotFound, "no such endpoint")
	}))
	mux.Handle("/", s.spa())

	var h http.Handler = mux
	h = httpx.SecurityHeaders(h)
	h = httpx.RealIP(s.Config.TrustedProxies)(h)
	return h
}

// Sweep drops idle rate-limit buckets.
func (s *Server) Sweep() {
	s.codeLimiter.Sweep()
	s.agentLimiter.Sweep()
}

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, 200, map[string]any{"org_name": s.Config.OrgName})
}

func (s *Server) checkCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := httpx.ReadJSON(r, &body); err != nil {
		httpx.Error(w, 400, "invalid request")
		return
	}
	code := stamp.NormalizeCode(body.Code)
	ss, ok := s.Sessions.LookupCode(code)
	if !stamp.IsCode(code) || !ok {
		httpx.Error(w, http.StatusNotFound, "That code isn't valid or has expired. Check it with your support technician.")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"code":            code,
		"org_name":        s.Config.OrgName,
		"tech_name":       ss.TechName,
		"code_expires_at": ss.CodeExpiresAt,
		"platforms":       s.Dist.Available(),
	})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	code := stamp.NormalizeCode(r.PathValue("code"))
	p, ok := dist.Lookup(r.PathValue("platform"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ss, ok := s.Sessions.LookupCode(code)
	if !ok {
		http.Error(w, "That code isn't valid or has expired.", http.StatusNotFound)
		return
	}
	st := stamp.Stamp{ServerURL: s.Config.PublicURL.String(), Code: code, OrgName: s.Config.OrgName}
	ctype := "application/octet-stream"
	if strings.HasSuffix(dist.Filename(p, code), ".zip") {
		ctype = "application/zip"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+dist.Filename(p, code)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := s.Dist.Write(w, p, st); err != nil {
		if errors.Is(err, dist.ErrUnavailable) {
			w.Header().Del("Content-Disposition")
			http.Error(w, "No agent build is installed for this platform.", http.StatusNotFound)
			return
		}
		s.Log.Error("serve agent", "platform", p.ID, "err", err)
		return
	}
	s.Sessions.RecordDownload(ss.ID, p.ID, httpx.ClientIP(r))
}

// Reusable assets have only public organization configuration, never session
// credentials. Authentication gates distribution, but is not embedded in it.
func (s *Server) agentDownloads(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, 200, map[string]any{"server_url": s.Config.PublicURL.String(), "platforms": s.Dist.Available()})
}

func (s *Server) downloadReusable(w http.ResponseWriter, r *http.Request) {
	p, ok := dist.Lookup(r.PathValue("platform"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	st := stamp.Stamp{ServerURL: s.Config.PublicURL.String(), OrgName: s.Config.OrgName}
	// Prepare before sending headers so invalid/missing builds return an error,
	// rather than a successful download of a partial executable.
	var out bytes.Buffer
	if err := s.Dist.Write(&out, p, st); err != nil {
		if errors.Is(err, dist.ErrUnavailable) {
			httpx.Error(w, 404, "No agent build is installed for this platform.")
			return
		}
		s.Log.Error("prepare reusable agent", "platform", p.ID, "err", err)
		httpx.Error(w, 500, "Could not prepare reusable agent. Check server build configuration.")
		return
	}
	ctype := "application/octet-stream"
	if p.OS == "darwin" {
		ctype = "application/zip"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+dist.Filename(p, "")+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out.Bytes())
}

func tech(r *http.Request) auth.Identity {
	id, _ := auth.FromContext(r.Context())
	return id
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, 200, s.Sessions.List(tech(r)))
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	ss, err := s.Sessions.Create(r.Context(), tech(r), httpx.ClientIP(r))
	if err != nil {
		s.Log.Error("create session", "err", err)
		httpx.Error(w, 500, "could not create session")
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{
		"id": ss.ID, "code": ss.Code,
		"code_expires_at": ss.CodeExpiresAt, "expires_in": int(time.Until(ss.CodeExpiresAt).Seconds()),
		"join_url": s.Config.PublicURL.String() + "/",
	})
}

func (s *Server) sessionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		httpx.Error(w, 404, "session not found or already ended")
	case errors.Is(err, session.ErrNotOwner):
		httpx.Error(w, 403, "this session belongs to another tech")
	default:
		httpx.Error(w, 500, "internal error")
	}
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Sessions.Get(r.PathValue("id"), tech(r))
	if err != nil {
		s.sessionErr(w, err)
		return
	}
	snap.Mine = true
	httpx.WriteJSON(w, 200, map[string]any{"session": snap})
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if err := s.Sessions.End(r.PathValue("id"), tech(r)); err != nil {
		s.sessionErr(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func paging(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (s *Server) auditSessions(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	list, err := s.Store.ListSupportSessions(r.Context(), store.ListFilter{Limit: limit, Offset: offset})
	if err != nil {
		httpx.Error(w, 500, "internal error")
		return
	}
	httpx.WriteJSON(w, 200, list)
}

func (s *Server) auditSessionEvents(w http.ResponseWriter, r *http.Request) {
	ev, err := s.Store.ListEvents(r.Context(), store.EventFilter{SessionID: r.PathValue("id"), Limit: 5000})
	if err != nil {
		httpx.Error(w, 500, "internal error")
		return
	}
	httpx.WriteJSON(w, 200, ev)
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	ev, err := s.Store.ListEvents(r.Context(), store.EventFilter{Limit: limit, Offset: offset})
	if err != nil {
		httpx.Error(w, 500, "internal error")
		return
	}
	httpx.WriteJSON(w, 200, ev)
}

func (s *Server) auditCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="simpleremote-sessions-`+time.Now().Format("2006-01-02")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "code", "state", "tech_name", "tech_email", "created_at", "connected_at", "consented_at",
		"ended_at", "end_reason", "agent_hostname", "agent_user", "agent_os", "agent_arch", "agent_version", "agent_ip"})
	ts := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	for offset := 0; ; offset += 1000 {
		list, err := s.Store.ListSupportSessions(r.Context(), store.ListFilter{Limit: 1000, Offset: offset})
		if err != nil {
			s.Log.Error("audit csv", "err", err)
			break
		}
		for _, ss := range list {
			_ = cw.Write(csvSafe([]string{ss.ID, ss.Code, ss.State, ss.TechName, ss.TechEmail, ts(ss.CreatedAt), ts(ss.ConnectedAt),
				ts(ss.ConsentedAt), ts(ss.EndedAt), ss.EndReason, ss.AgentHostname, ss.AgentUser, ss.AgentOS, ss.AgentArch,
				ss.AgentVersion, ss.AgentIP}))
		}
		if len(list) < 1000 {
			break
		}
	}
	cw.Flush()
}

// csvSafe neutralizes values that spreadsheet apps would run as formulas.
// Hostnames and usernames come from the end user's computer.
func csvSafe(row []string) []string {
	for i, v := range row {
		if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
			row[i] = "'" + v
		}
	}
	return row
}

// spa serves the built web app. Unknown paths get index.html so client-side
// routes work. Tech console pages redirect to single sign-on when auto-login
// is on and nobody is signed in.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		if p != "/" && !strings.HasPrefix(p, "/tech") {
			if f, err := fs.Stat(s.Web, strings.TrimPrefix(p, "/")); err == nil && !f.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		if (p == "/tech" || strings.HasPrefix(p, "/tech/")) && !strings.HasPrefix(p, "/tech/login") &&
			s.Config.OIDC.Enabled() && s.Config.OIDC.AutoLogin {
			if _, _, ok := s.Auth.Lookup(r); !ok {
				http.Redirect(w, r, "/auth/oidc/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
				return
			}
		}
		index, err := fs.ReadFile(s.Web, "index.html")
		if err != nil {
			http.Error(w, "The web UI has not been built. Run `npm ci && npm run build` in web/.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
