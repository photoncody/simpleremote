// Package session manages support sessions: one-time codes, the consent
// state machine, and the WebSocket hub that relays WebRTC signaling between
// the tech's browser and the agent.
//
// Live connection state is kept in memory, so the server is meant to run as a
// single instance. Sessions that were live when the server stopped are marked
// ended at startup.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/photoncody/simpleremote/internal/proto"
	"github.com/photoncody/simpleremote/server/internal/auth"
	"github.com/photoncody/simpleremote/server/internal/config"
	"github.com/photoncody/simpleremote/server/internal/store"
	"github.com/photoncody/simpleremote/server/internal/turn"
)

// Errors returned to callers.
var (
	ErrNotFound  = errors.New("session not found")
	ErrNotOwner  = errors.New("session belongs to another tech")
	ErrCodeUsed  = errors.New("code is no longer valid")
	ErrNotActive = errors.New("session is not active")
)

// Grace periods before a missing peer ends the session.
var (
	AgentGrace = 60 * time.Second
	TechGrace  = 5 * time.Minute
)

// Manager owns all live sessions.
type Manager struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger
	now   func() time.Time

	mu      sync.Mutex
	rooms   map[string]*room // by session ID
	byCode  map[string]*room // pending sessions only
	byToken map[string]*room // agent resume tokens
}

// New creates a manager and ends sessions left over from a previous run.
func New(ctx context.Context, cfg *config.Config, st *store.Store, log *slog.Logger) (*Manager, error) {
	m := &Manager{cfg: cfg, store: st, log: log, now: time.Now,
		rooms: map[string]*room{}, byCode: map[string]*room{}, byToken: map[string]*room{}}
	n, err := st.EndAllLive(ctx, m.now(), "server restarted")
	if err != nil {
		return nil, err
	}
	if n > 0 {
		log.Info("ended sessions left over from previous run", "count", n)
	}
	return m, nil
}

// room is one live session.
type room struct {
	m  *Manager
	mu sync.Mutex

	sess        store.SupportSession
	resumeToken string
	// tokenAcked is set once the agent has shown it received its resume
	// token. Until then the code stays usable by an agent that reconnects,
	// so a dropped first handshake doesn't strand the session.
	tokenAcked bool
	consented  bool
	ended      bool

	tech        *peer
	agent       *peer
	agentInfo   *proto.AgentInfo
	agentLostAt time.Time
	techLostAt  time.Time
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func newCode() string {
	// 100000..999999: six digits that never start with zero, easy to read
	// out over the phone. Guessing is limited by per-IP rate limits, the
	// short CODE_TTL, single use, and the user's consent prompt.
	n, _ := rand.Int(rand.Reader, big.NewInt(900_000))
	return fmt.Sprintf("%06d", n.Int64()+100_000)
}

// Create issues a new session code for tech.
func (m *Manager) Create(ctx context.Context, tech auth.Identity, ip string) (store.SupportSession, error) {
	now := m.now()
	for attempt := 0; attempt < 10; attempt++ {
		ss := store.SupportSession{
			ID: newID(), Code: newCode(), State: proto.StatePending, Live: true,
			TechSubject: tech.Subject, TechName: tech.Name, TechEmail: tech.Email,
			CreatedAt: now, CodeExpiresAt: now.Add(m.cfg.CodeTTL),
		}
		err := m.store.InsertSupportSession(ctx, ss)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return ss, err
		}
		r := &room{m: m, sess: ss}
		m.mu.Lock()
		m.rooms[ss.ID] = r
		m.byCode[ss.Code] = r
		m.mu.Unlock()
		m.audit(ss.ID, tech.Label(), "created", "code "+ss.Code, ip)
		return ss, nil
	}
	return store.SupportSession{}, errors.New("could not allocate a unique code")
}

// LookupCode returns the pending session for a code the end user typed.
func (m *Manager) LookupCode(code string) (store.SupportSession, bool) {
	m.mu.Lock()
	r := m.byCode[code]
	m.mu.Unlock()
	if r == nil {
		return store.SupportSession{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended || r.sess.State != proto.StatePending || m.now().After(r.sess.CodeExpiresAt) {
		return store.SupportSession{}, false
	}
	return r.sess, true
}

// Get returns a live session if tech owns it.
func (m *Manager) Get(id string, tech auth.Identity) (Snapshot, error) {
	r, err := m.ownedRoom(id, tech)
	if err != nil {
		return Snapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshot(), nil
}

// Snapshot is a session's live state for the API.
type Snapshot struct {
	store.SupportSession
	AgentConnected bool             `json:"agent_connected"`
	TechConnected  bool             `json:"tech_connected"`
	Agent          *proto.AgentInfo `json:"agent,omitempty"`
	Mine           bool             `json:"mine"`
}

func (r *room) snapshot() Snapshot {
	return Snapshot{SupportSession: r.sess, AgentConnected: r.agent != nil, TechConnected: r.tech != nil, Agent: r.agentInfo}
}

// List returns all live sessions. Mine is set for sessions tech owns.
func (m *Manager) List(tech auth.Identity) []Snapshot {
	m.mu.Lock()
	rooms := make([]*room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	out := make([]Snapshot, 0, len(rooms))
	for _, r := range rooms {
		r.mu.Lock()
		s := r.snapshot()
		s.Mine = r.sess.TechSubject == tech.Subject
		r.mu.Unlock()
		out = append(out, s)
	}
	return out
}

func (m *Manager) ownedRoom(id string, tech auth.Identity) (*room, error) {
	m.mu.Lock()
	r := m.rooms[id]
	m.mu.Unlock()
	if r == nil {
		return nil, ErrNotFound
	}
	if r.sess.TechSubject != tech.Subject {
		return nil, ErrNotOwner
	}
	return r, nil
}

// End stops a session at the tech's request.
func (m *Manager) End(id string, tech auth.Identity) error {
	r, err := m.ownedRoom(id, tech)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endLocked(proto.StateEnded, "ended by tech", tech.Label())
	return nil
}

// RecordDownload notes that the end user downloaded the agent.
func (m *Manager) RecordDownload(sessionID, platform, ip string) {
	m.audit(sessionID, "end user", "downloaded", platform, ip)
}

// endLocked finishes the session. r.mu must be held.
func (r *room) endLocked(state, reason, actor string) {
	if r.ended {
		return
	}
	r.ended = true
	now := r.m.now()
	r.sess.State, r.sess.Live, r.sess.EndedAt, r.sess.EndReason = state, false, now, reason
	if err := r.m.store.UpdateSupportSession(context.Background(), r.sess); err != nil {
		r.m.log.Error("update session", "id", r.sess.ID, "err", err)
	}
	kind := "ended"
	if state == proto.StateExpired {
		kind = "expired"
	}
	r.m.audit(r.sess.ID, actor, kind, reason, "")

	msg, _ := proto.Marshal(proto.TypeEnd, proto.End{Reason: reason})
	for _, p := range []*peer{r.tech, r.agent} {
		if p != nil {
			p.send(msg)
			p.closeAfterFlush()
		}
	}
	r.tech, r.agent = nil, nil

	r.m.mu.Lock()
	delete(r.m.rooms, r.sess.ID)
	if r.m.byCode[r.sess.Code] == r {
		delete(r.m.byCode, r.sess.Code)
	}
	if r.resumeToken != "" {
		delete(r.m.byToken, r.resumeToken)
	}
	r.m.mu.Unlock()
}

// saveLocked persists the session row. r.mu must be held.
func (r *room) saveLocked() {
	if err := r.m.store.UpdateSupportSession(context.Background(), r.sess); err != nil {
		r.m.log.Error("update session", "id", r.sess.ID, "err", err)
	}
}

// broadcastStatusLocked tells the tech about a state change.
func (r *room) broadcastStatusLocked() {
	if r.tech == nil {
		return
	}
	msg, _ := proto.Marshal(proto.TypeStatus, proto.Status{
		State: r.sess.State, AgentConnected: r.agent != nil, Agent: r.agentInfo, EndReason: r.sess.EndReason,
	})
	r.tech.send(msg)
}

func (r *room) helloLocked(forAgent bool) []byte {
	h := proto.Hello{
		SessionID: r.sess.ID, OrgName: r.m.cfg.OrgName, TechName: r.sess.TechName, TechEmail: r.sess.TechEmail,
		ICEServers: turn.Servers(r.m.cfg.ICE, r.sess.ID, r.m.now()),
	}
	if forAgent {
		h.ResumeToken = r.resumeToken
		h.Resumed = r.consented
	}
	msg, _ := proto.Marshal(proto.TypeHello, h)
	return msg
}

func (m *Manager) audit(sessionID, actor, kind, detail, ip string) {
	if err := m.store.AddEvent(context.Background(), store.Event{SessionID: sessionID, Actor: actor, Kind: kind, Detail: detail, IP: ip}); err != nil {
		m.log.Error("audit", "err", err)
	}
}

// Reap expires unused codes, enforces the maximum session length, and ends
// sessions whose peers have been gone too long. Call it periodically.
func (m *Manager) Reap() {
	m.mu.Lock()
	rooms := make([]*room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	now := m.now()
	for _, r := range rooms {
		r.mu.Lock()
		switch {
		case r.sess.State == proto.StatePending && now.After(r.sess.CodeExpiresAt):
			r.endLocked(proto.StateExpired, "code expired before use", "system")
		case now.Sub(r.sess.CreatedAt) > m.cfg.SessionMaxDuration:
			r.endLocked(proto.StateEnded, "maximum session length reached", "system")
		case r.sess.State != proto.StatePending && r.agent == nil && !r.agentLostAt.IsZero() && now.Sub(r.agentLostAt) > AgentGrace:
			r.endLocked(proto.StateEnded, "agent disconnected", "system")
		case r.sess.State == proto.StateActive && r.tech == nil && !r.techLostAt.IsZero() && now.Sub(r.techLostAt) > TechGrace:
			r.endLocked(proto.StateEnded, "tech disconnected", "system")
		}
		r.mu.Unlock()
	}
}
