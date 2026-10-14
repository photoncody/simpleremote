package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/photoncody/simpleremote/internal/proto"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/httpx"
)

const (
	maxMessage   = 1 << 20
	pingInterval = 25 * time.Second
	writeTimeout = 10 * time.Second
)

// peer is one WebSocket connection with a buffered writer goroutine.
type peer struct {
	ws       *websocket.Conn
	out      chan []byte
	done     chan struct{}
	finished chan struct{}
	once     sync.Once
	closing  atomic.Bool
}

func newPeer(ws *websocket.Conn) *peer {
	p := &peer{ws: ws, out: make(chan []byte, 256), done: make(chan struct{}), finished: make(chan struct{})}
	go p.writeLoop()
	return p
}

func (p *peer) writeLoop() {
	defer close(p.finished)
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case b := <-p.out:
			if b == nil { // close sentinel
				p.close(websocket.StatusNormalClosure, "session ended")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := p.ws.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				p.close(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := p.ws.Ping(ctx)
			cancel()
			if err != nil {
				p.close(websocket.StatusGoingAway, "ping failed")
				return
			}
		case <-p.done:
			return
		}
	}
}

// send queues a message. A peer that can't keep up is disconnected.
func (p *peer) send(b []byte) {
	if p.closing.Load() {
		return
	}
	select {
	case p.out <- b:
	case <-p.done:
	default:
		p.close(websocket.StatusPolicyViolation, "too slow")
	}
}

// closeAfterFlush closes the connection once queued messages are written.
func (p *peer) closeAfterFlush() {
	if p.closing.Swap(true) {
		return
	}
	select {
	case p.out <- nil:
	default:
		p.close(websocket.StatusNormalClosure, "session ended")
	}
}

// shutdown is deferred by handlers. It lets a pending closeAfterFlush finish
// writing before tearing the connection down.
func (p *peer) shutdown() {
	if p.closing.Load() {
		select {
		case <-p.finished:
		case <-time.After(2 * time.Second):
		}
	}
	p.close(websocket.StatusNormalClosure, "")
}

func (p *peer) close(code websocket.StatusCode, reason string) {
	p.once.Do(func() {
		close(p.done)
		go p.ws.Close(code, reason)
	})
}

func (p *peer) sendError(msg string) {
	b, _ := proto.Marshal(proto.TypeError, proto.Error{Message: msg})
	p.send(b)
}

// ---- agent -----------------------------------------------------------------

// AgentHandler serves the agent's signaling WebSocket. The agent proves
// which session it belongs to with the one-time code (first connection) or
// the resume token it was given (reconnects).
func (m *Manager) AgentHandler(limiter *httpx.Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httpx.ClientIP(r)
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ws.SetReadLimit(maxMessage)
		p := newPeer(ws)
		defer p.shutdown()

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		var env proto.Envelope
		err = readJSON(ctx, ws, &env)
		cancel()
		var join proto.Join
		if err != nil || env.Type != proto.TypeJoin || json.Unmarshal(env.Data, &join) != nil {
			p.sendError("expected join message")
			p.closeAfterFlush()
			return
		}
		if join.ProtocolVersion != proto.Version {
			p.sendError("this agent version is not compatible with the server; download it again")
			p.closeAfterFlush()
			return
		}
		if !limiter.Allow(ip) {
			p.sendError("too many attempts, wait a minute and try again")
			p.closeAfterFlush()
			return
		}

		rm, err := m.attachAgent(p, join, ip)
		if err != nil {
			p.sendError(err.Error())
			p.closeAfterFlush()
			return
		}

		for {
			var env proto.Envelope
			if err := readJSON(r.Context(), ws, &env); err != nil {
				break
			}
			rm.onAgentMessage(p, env)
		}

		rm.mu.Lock()
		if rm.agent == p {
			rm.agent = nil
			rm.agentLostAt = m.now()
			rm.broadcastStatusLocked()
		}
		rm.mu.Unlock()
	})
}

func (m *Manager) attachAgent(p *peer, join proto.Join, ip string) (*room, error) {
	m.mu.Lock()
	var rm *room
	resumed := false
	switch {
	case join.ResumeToken != "":
		rm = m.byToken[join.ResumeToken]
		resumed = true
	case join.Code != "":
		rm = m.byCode[join.Code]
		if rm != nil {
			// The code is single-use: consume it now.
			delete(m.byCode, join.Code)
		}
	}
	m.mu.Unlock()
	if rm == nil {
		return nil, ErrCodeUsed
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.ended {
		return nil, ErrCodeUsed
	}
	now := m.now()
	if !resumed {
		if rm.sess.State != proto.StatePending || now.After(rm.sess.CodeExpiresAt) {
			return nil, ErrCodeUsed
		}
		rm.sess.State = proto.StateAwaitingConsent
		rm.sess.ConnectedAt = now
		rm.sess.AgentIP = ip
		rm.resumeToken = newID()
		m.mu.Lock()
		m.byToken[rm.resumeToken] = rm
		m.mu.Unlock()
		rm.saveLocked()
		m.audit(rm.sess.ID, "end user", "agent_connected", "", ip)
	} else {
		if rm.agent != nil {
			rm.agent.closeAfterFlush()
		}
		m.audit(rm.sess.ID, "end user", "agent_reconnected", "", ip)
	}
	rm.agent = p
	rm.agentLostAt = time.Time{}
	p.send(rm.helloLocked(true))
	rm.broadcastStatusLocked()
	return rm, nil
}

var eventKindRE = regexp.MustCompile(`^[a-z_]{1,40}$`)

func (r *room) onAgentMessage(p *peer, env proto.Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended || r.agent != p {
		return
	}
	switch env.Type {
	case proto.TypeAgentInfo:
		var info proto.AgentInfo
		if json.Unmarshal(env.Data, &info) != nil {
			return
		}
		info.Hostname, info.Username = clip(info.Hostname, 255), clip(info.Username, 255)
		info.OS, info.Arch, info.Version = clip(info.OS, 64), clip(info.Arch, 32), clip(info.Version, 64)
		r.agentInfo = &info
		r.sess.AgentHostname, r.sess.AgentOS, r.sess.AgentArch = info.Hostname, info.OS, info.Arch
		r.sess.AgentUser, r.sess.AgentVersion = info.Username, info.Version
		r.saveLocked()
		r.broadcastStatusLocked()

	case proto.TypeConsent:
		var c proto.Consent
		if json.Unmarshal(env.Data, &c) != nil || r.sess.State != proto.StateAwaitingConsent {
			return
		}
		if !c.Allowed {
			r.m.audit(r.sess.ID, "end user", "consent_denied", "", "")
			r.endLocked(proto.StateEnded, "the user declined the connection", "end user")
			return
		}
		r.consented = true
		r.sess.State = proto.StateActive
		r.sess.ConsentedAt = r.m.now()
		r.saveLocked()
		r.m.audit(r.sess.ID, "end user", "consent_granted", "", "")
		r.broadcastStatusLocked()

	case proto.TypeAnswer, proto.TypeICE:
		if r.sess.State == proto.StateActive && r.tech != nil {
			r.tech.send(rawEnvelope(env))
		}

	case proto.TypeEnd:
		r.endLocked(proto.StateEnded, "ended by the user", "end user")

	case proto.TypeEvent:
		var e proto.Event
		if json.Unmarshal(env.Data, &e) == nil && eventKindRE.MatchString(e.Kind) {
			r.m.audit(r.sess.ID, "end user", e.Kind, clip(e.Detail, 1000), "")
		}
	}
}

// ---- tech ------------------------------------------------------------------

// TechHandler serves the tech console's signaling WebSocket for one session.
func (m *Manager) TechHandler(a *auth.Service, publicOrigin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _, ok := a.Lookup(r)
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "not signed in")
			return
		}
		// Browsers always send Origin on WebSocket requests; require ours to
		// stop other sites from opening sockets with the tech's cookie.
		if r.Header.Get("Origin") == "" || !httpx.SameOrigin(r, publicOrigin) {
			httpx.Error(w, http.StatusForbidden, "cross-origin request")
			return
		}
		rm, err := m.ownedRoom(r.PathValue("id"), id)
		if err != nil {
			status := http.StatusNotFound
			if errors.Is(err, ErrNotOwner) {
				status = http.StatusForbidden
			}
			httpx.Error(w, status, err.Error())
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // origin checked above
		if err != nil {
			return
		}
		ws.SetReadLimit(maxMessage)
		p := newPeer(ws)
		defer p.shutdown()

		rm.mu.Lock()
		if rm.ended {
			rm.mu.Unlock()
			p.sendError("session has ended")
			p.closeAfterFlush()
			return
		}
		if rm.tech != nil {
			rm.tech.sendError("this session was opened in another window")
			rm.tech.closeAfterFlush()
		}
		rm.tech = p
		rm.techLostAt = time.Time{}
		p.send(rm.helloLocked(false))
		rm.broadcastStatusLocked()
		rm.mu.Unlock()
		m.audit(rm.sess.ID, id.Label(), "tech_joined", "", httpx.ClientIP(r))

		for {
			var env proto.Envelope
			if err := readJSON(r.Context(), ws, &env); err != nil {
				break
			}
			rm.onTechMessage(p, id, env)
		}

		rm.mu.Lock()
		if rm.tech == p {
			rm.tech = nil
			rm.techLostAt = m.now()
		}
		rm.mu.Unlock()
	})
}

func (r *room) onTechMessage(p *peer, id auth.Identity, env proto.Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended || r.tech != p {
		return
	}
	switch env.Type {
	case proto.TypeOffer, proto.TypeICE:
		// Nothing reaches the agent's WebRTC stack until the user consents.
		if r.sess.State == proto.StateActive && r.agent != nil {
			r.agent.send(rawEnvelope(env))
		}
	case proto.TypeElevateRequest:
		if r.sess.State == proto.StateActive && r.agent != nil {
			r.agent.send(rawEnvelope(env))
			r.m.audit(r.sess.ID, id.Label(), "elevation_requested", "", "")
		}
	case proto.TypeEnd:
		r.endLocked(proto.StateEnded, "ended by tech", id.Label())
	case proto.TypeEvent:
		var e proto.Event
		if json.Unmarshal(env.Data, &e) == nil && eventKindRE.MatchString(e.Kind) {
			r.m.audit(r.sess.ID, id.Label(), e.Kind, clip(e.Detail, 1000), "")
		}
	}
}

// ---- helpers ---------------------------------------------------------------

func readJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	typ, b, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return errors.New("expected text message")
	}
	return json.Unmarshal(b, v)
}

func rawEnvelope(env proto.Envelope) []byte {
	b, _ := json.Marshal(env)
	return b
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
