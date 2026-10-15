// Package core runs one support session on the end user's computer: it joins
// the session on the server, asks the user for consent, and then serves the
// tech's WebRTC connection (screen, input, clipboard, chat, and files).
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/photoncody/simpleremote/agent/internal/persist"
	"github.com/photoncody/simpleremote/agent/internal/platform"
	"github.com/photoncody/simpleremote/internal/proto"
)

// UI is implemented by the agent's window. Methods are called from
// background goroutines.
type UI interface {
	Connecting(server string)
	// AskConsent shows the Allow/Deny prompt. The UI answers by calling
	// Session.Respond.
	AskConsent(h proto.Hello)
	// NeedPermissions is shown on macOS when Screen Recording or
	// Accessibility is missing after the user allowed the session.
	NeedPermissions(p platform.Permissions)
	Active(h proto.Hello)
	Reconnecting()
	Chat(from, text string)
	Activity(text string)
	// ElevationRequested asks the user whether to relaunch as administrator.
	ElevationRequested(techName string)
	// PersistState reports whether "stay connected after restart" is on, so
	// the window can show it.
	PersistState(on bool)
	Ended(reason string)
}

// Options configure a session.
type Options struct {
	ServerURL   string
	Code        string
	ResumeToken string
	Version     string
	UI          UI
	Log         *slog.Logger
	// AutoAllow skips the consent prompt. Only end-to-end test builds set it.
	AutoAllow bool
	// FromAutostart is set when the agent was relaunched after a restart to
	// resume a persisted session. The network is often not up yet at login,
	// so the first connection is retried rather than failing immediately.
	FromAutostart bool
	// Owner is the account of the user who allowed the session; it defaults
	// to the current user. Reboot reconnect is only set up while the agent
	// runs as that user, so it can never start in someone else's login.
	Owner string
}

// Session is one connection to the server.
type Session struct {
	opts Options
	log  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	ws          *websocket.Conn
	hello       proto.Hello
	resumeToken string
	consented   bool
	ended       bool
	handoff     bool
	rtc         *peer
	// consentWaiting is set while a consent prompt is open, so a signaling
	// reconnect doesn't start a second waiter.
	consentWaiting bool
	// pendingICE holds candidates that arrive before the peer connection
	// has the tech's offer installed.
	pendingICE []proto.ICECandidate
	// persistent mirrors the server's reboot-reconnect state.
	persistent bool

	consentCh chan bool
}

// New creates a session. Call Run to start it.
func New(opts Options) *Session {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Owner == "" {
		opts.Owner = platform.Username()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{opts: opts, log: opts.Log, ctx: ctx, cancel: cancel,
		resumeToken: opts.ResumeToken, consentCh: make(chan bool, 1)}
}

// Hello returns the session details received from the server.
func (s *Session) Hello() proto.Hello {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hello
}

func wsURL(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported server URL %q", server)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws/agent"
	return u.String(), nil
}

// Run connects and serves the session until it ends.
func (s *Session) Run() {
	defer s.cleanup()
	s.opts.UI.Connecting(s.opts.ServerURL)
	var err error
	if s.opts.FromAutostart {
		// Just logged in after a restart: the network is often not ready yet,
		// so keep trying instead of treating the first failure as final.
		err = s.retryConnect(persistedReconnectWindow)
	} else {
		err = s.connect(true)
	}
	for err == nil || errors.Is(err, errDisconnected) {
		if s.isEnded() {
			return
		}
		// The connection dropped. Reconnect with the resume token while the
		// server keeps the session open.
		s.opts.UI.Reconnecting()
		err = s.reconnect()
	}
	if s.isEnded() {
		return
	}
	s.finish(err.Error(), false)
}

var errDisconnected = errors.New("disconnected from server")

// How long the agent keeps trying to reach the server. A session with reboot
// reconnect on is held open by the server for REBOOT_GRACE, so its agent
// keeps trying for longer before giving up.
const (
	reconnectWindow          = 50 * time.Second
	persistedReconnectWindow = 5 * time.Minute
)

func (s *Session) reconnect() error {
	s.mu.Lock()
	window := reconnectWindow
	if s.persistent {
		window = persistedReconnectWindow
	}
	s.mu.Unlock()
	return s.retryConnect(window)
}

// retryConnect dials with backoff for up to window, stopping early when the
// server reports something final, such as the session having ended.
func (s *Session) retryConnect(window time.Duration) error {
	deadline := time.Now().Add(window)
	delay := time.Second
	for time.Now().Before(deadline) {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(delay):
		}
		err := s.connect(false)
		if err == nil || errors.Is(err, errDisconnected) {
			return err
		}
		var fatal fatalError
		if errors.As(err, &fatal) {
			return err
		}
		s.log.Info("reconnect failed", "err", err)
		delay = min(delay*2, 8*time.Second)
	}
	return errors.New("lost connection to the server")
}

type fatalError struct{ msg string }

func (f fatalError) Error() string { return f.msg }

// connect dials the server, joins, and processes messages until the socket
// closes. It returns errDisconnected if the connection dropped unexpectedly.
func (s *Session) connect(first bool) error {
	u, err := wsURL(s.opts.ServerURL)
	if err != nil {
		return fatalError{err.Error()}
	}
	dctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	ws, _, err := websocket.Dial(dctx, u, nil)
	cancel()
	if err != nil {
		if first {
			return fatalError{fmt.Sprintf("Could not reach %s. Check your internet connection and try again.", s.opts.ServerURL)}
		}
		return err
	}
	ws.SetReadLimit(1 << 20)

	s.mu.Lock()
	join := proto.Join{ProtocolVersion: proto.Version, ResumeToken: s.resumeToken}
	if join.ResumeToken == "" {
		join.Code = s.opts.Code
	}
	s.ws = ws
	s.mu.Unlock()
	if err := s.send(proto.TypeJoin, join); err != nil {
		return err
	}

	for {
		typ, data, err := ws.Read(s.ctx)
		if err != nil {
			s.mu.Lock()
			if s.ws == ws {
				s.ws = nil
			}
			ended, handoff := s.ended, s.handoff
			s.mu.Unlock()
			if ended || handoff || s.ctx.Err() != nil {
				return nil
			}
			return errDisconnected
		}
		if typ != websocket.MessageText {
			continue
		}
		var env proto.Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		if err := s.onSignal(env); err != nil {
			ws.Close(websocket.StatusNormalClosure, "")
			return err
		}
	}
}

func (s *Session) send(typ string, v any) error {
	b, err := proto.Marshal(typ, v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ws := s.ws
	s.mu.Unlock()
	if ws == nil {
		return errDisconnected
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, b)
}

func (s *Session) audit(kind, detail string) {
	_ = s.send(proto.TypeEvent, proto.Event{Kind: kind, Detail: detail})
}

func (s *Session) onSignal(env proto.Envelope) error {
	switch env.Type {
	case proto.TypeHello:
		var h proto.Hello
		if err := json.Unmarshal(env.Data, &h); err != nil {
			return fatalError{"invalid hello from server"}
		}
		s.mu.Lock()
		s.hello = h
		if h.ResumeToken != "" {
			s.resumeToken = h.ResumeToken
		}
		// The user allowed the session but the server never got the answer
		// (the connection dropped while sending): send it again.
		resendConsent := s.consented && !h.Resumed
		alreadyConsented := s.consented || h.Resumed
		s.consented = alreadyConsented
		startWaiter := !alreadyConsented && !s.consentWaiting
		if startWaiter {
			s.consentWaiting = true
		}
		s.mu.Unlock()
		_ = s.send(proto.TypeAgentInfo, proto.AgentInfo{
			Hostname: platform.Hostname(), OS: runtime.GOOS, Arch: runtime.GOARCH,
			Username: platform.Username(), Version: s.opts.Version, Elevated: platform.IsElevated(),
		})
		if resendConsent {
			if err := s.send(proto.TypeConsent, proto.Consent{Allowed: true}); err != nil {
				s.log.Warn("resend consent", "err", err)
			}
		}
		if alreadyConsented {
			s.opts.UI.Active(h)
			// Re-arm with a fresh expiry when resuming a persisted session.
			s.applyPersist(h.Persist, time.Unix(h.PersistUntil, 0))
			if p := platform.CheckPermissions(); !p.OK() {
				s.opts.UI.NeedPermissions(p)
			}
			return nil
		}
		if startWaiter {
			go s.awaitConsent(h)
		}

	case proto.TypeOffer:
		var sdp proto.SDP
		if err := json.Unmarshal(env.Data, &sdp); err != nil {
			return nil
		}
		s.mu.Lock()
		ok := s.consented
		s.mu.Unlock()
		if !ok {
			// The server never relays offers before consent; ignore anyway.
			return nil
		}
		// Handled in order, so ICE candidates that follow the offer find the
		// peer connection ready.
		s.handleOffer(sdp)

	case proto.TypeICE:
		var c proto.ICECandidate
		if json.Unmarshal(env.Data, &c) == nil {
			s.mu.Lock()
			p := s.rtc
			if p == nil && len(s.pendingICE) < 100 {
				s.pendingICE = append(s.pendingICE, c)
			}
			s.mu.Unlock()
			if p != nil {
				p.addCandidate(c)
			}
		}

	case proto.TypeElevateRequest:
		if platform.CanElevate() {
			s.opts.UI.ElevationRequested(s.Hello().TechName)
		}

	case proto.TypePersist:
		var pm proto.Persist
		if json.Unmarshal(env.Data, &pm) == nil {
			s.applyPersist(pm.On, time.Unix(pm.Until, 0))
		}

	case proto.TypeEnd:
		var e proto.End
		_ = json.Unmarshal(env.Data, &e)
		reason := e.Reason
		if reason == "" {
			reason = "The session has ended."
		}
		s.finish(reason, false)

	case proto.TypeError:
		var e proto.Error
		_ = json.Unmarshal(env.Data, &e)
		return fatalError{e.Message}
	}
	return nil
}

func (s *Session) awaitConsent(h proto.Hello) {
	if s.opts.AutoAllow {
		s.consentCh <- true
	} else {
		s.opts.UI.AskConsent(h)
	}
	var allowed bool
	select {
	case allowed = <-s.consentCh:
	case <-time.After(5 * time.Minute):
		allowed = false
	case <-s.ctx.Done():
		return
	}
	s.mu.Lock()
	s.consentWaiting = false
	s.mu.Unlock()
	if !allowed {
		_ = s.send(proto.TypeConsent, proto.Consent{Allowed: false})
		s.finish("You declined the connection.", false)
		return
	}
	s.mu.Lock()
	s.consented = true
	s.mu.Unlock()
	if err := s.send(proto.TypeConsent, proto.Consent{Allowed: true}); err != nil {
		s.log.Warn("send consent", "err", err)
	}
	s.opts.UI.Active(h)
	// Ask for missing macOS permissions only after consent is recorded, so
	// the restart this needs can resume the session.
	if p := platform.CheckPermissions(); !p.OK() {
		platform.RequestPermissions()
		s.opts.UI.NeedPermissions(p)
	}
}

// applyPersist arms or removes reboot reconnect to match the server. When on,
// it writes the resume file and installs the per-user autostart so the agent
// rejoins this session after the computer restarts.
func (s *Session) applyPersist(on bool, until time.Time) {
	s.mu.Lock()
	was := s.persistent
	s.persistent = on
	consented := s.consented
	token, server, org := s.resumeToken, s.opts.ServerURL, s.hello.OrgName
	s.mu.Unlock()

	if !on {
		// Always clear it, even when this process never set it up: after a
		// restart the arrangement was made by the previous process, and the
		// tech may have turned it off while the computer was rebooting.
		persist.DisarmFor(token)
		s.opts.UI.PersistState(false)
		if was {
			s.opts.UI.Activity("Reconnect-after-restart turned off.")
		}
		return
	}

	// Only ever set this up for the user who allowed the session, and only
	// once they have allowed it.
	if user := platform.Username(); user != s.opts.Owner {
		s.refusePersist(fmt.Sprintf("it is running as %s, but %s allowed the session", user, s.opts.Owner))
		return
	}
	if !consented {
		s.refusePersist("the user has not allowed the session yet")
		return
	}
	if until.IsZero() || until.Before(time.Now()) {
		until = time.Now().Add(10 * time.Minute)
	}
	err := persist.Arm(persist.Info{
		ServerURL: server, ResumeToken: token, OrgName: org, Expires: until, Owner: s.opts.Owner,
	})
	if err != nil {
		s.log.Warn("set up reboot reconnect", "err", err)
		s.refusePersist(err.Error())
		return
	}
	s.opts.UI.PersistState(true)
	if !was {
		s.opts.UI.Activity("This computer will reconnect to your technician after a restart, until the session ends.")
	}
}

// refusePersist reports back that reboot reconnect is not in place, so the
// server and the tech's toggle match what is really on this computer.
func (s *Session) refusePersist(why string) {
	s.mu.Lock()
	s.persistent = false
	token := s.resumeToken
	s.mu.Unlock()
	persist.DisarmFor(token)
	s.opts.UI.PersistState(false)
	s.notifyTech("This computer will not reconnect after a restart: " + why + ".")
	// Tell the server, so it stops holding the session open for a restart
	// that will never come and the toggle goes back to off.
	if err := s.send(proto.TypePersist, proto.Persist{On: false}); err != nil {
		s.log.Warn("report reboot reconnect off", "err", err)
	}
}

// Respond records the user's answer to the consent prompt.
func (s *Session) Respond(allowed bool) {
	select {
	case s.consentCh <- allowed:
	default:
	}
}

// End stops the session at the user's request.
func (s *Session) End() {
	_ = s.send(proto.TypeEnd, proto.End{Reason: "ended by the user"})
	s.finish("You ended the session.", true)
}

// SendChat sends a chat message to the tech.
func (s *Session) SendChat(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	p := s.rtc
	s.mu.Unlock()
	if p == nil || !p.sendControl(proto.Control{T: proto.CtlChat, Text: text}) {
		s.opts.UI.Activity("Chat isn't connected yet.")
		return
	}
	s.opts.UI.Chat("You", text)
}

// DeclineElevation tells the tech the user said no.
func (s *Session) DeclineElevation() {
	s.audit("elevation_declined", "")
	s.notifyTech("The user declined to restart as administrator.")
}

// Elevate relaunches the agent as administrator. On success this process
// hands the session over to the new one and should exit.
func (s *Session) Elevate() error {
	err := s.handoffTo(platform.RelaunchElevated, "elevation_accepted")
	if errors.Is(err, platform.ErrCancelled) {
		s.DeclineElevation()
	}
	return err
}

// Restart relaunches the agent, for example so macOS applies a newly granted
// Screen Recording permission. On success this process hands the session
// over to the new one and should exit.
func (s *Session) Restart() error {
	return s.handoffTo(platform.Relaunch, "agent_restarted")
}

// handoffTo starts a new agent process that resumes this session with the
// resume token, then disconnects without ending the session.
func (s *Session) handoffTo(relaunch func([]string) error, auditKind string) error {
	s.mu.Lock()
	token, server, org := s.resumeToken, s.opts.ServerURL, s.hello.OrgName
	consented := s.consented
	s.mu.Unlock()
	if token == "" || !consented {
		return errors.New("not connected")
	}
	// Pass the consenting user through: the new process may run as a
	// different account (an elevated relaunch can), and it must not set up
	// reboot reconnect in that account's login.
	args := []string{"--server", server, "--resume", token, "--org", org, "--owner", s.opts.Owner}
	if err := relaunch(args); err != nil {
		return err
	}
	s.audit(auditKind, "")
	s.mu.Lock()
	s.handoff = true
	ws := s.ws
	s.mu.Unlock()
	if ws != nil {
		ws.Close(websocket.StatusNormalClosure, "handing over to a new agent process")
	}
	s.cancel()
	return nil
}

func (s *Session) notifyTech(text string) {
	s.mu.Lock()
	p := s.rtc
	s.mu.Unlock()
	if p != nil {
		p.sendControl(proto.Control{T: proto.CtlNotice, Text: text})
	}
}

func (s *Session) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended || s.handoff
}

// finish ends the session locally and tells the UI.
func (s *Session) finish(reason string, byUser bool) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	ws := s.ws
	token := s.resumeToken
	s.mu.Unlock()
	// The session is over, so clear its reboot-reconnect arrangement. This is
	// a no-op when nothing was set up, and is skipped on a handoff (which
	// sets s.handoff and never reaches finish).
	persist.DisarmFor(token)
	if ws != nil {
		ws.Close(websocket.StatusNormalClosure, "")
	}
	s.cancel()
	s.opts.UI.Ended(reason)
}

func (s *Session) cleanup() {
	s.mu.Lock()
	p := s.rtc
	s.rtc = nil
	s.mu.Unlock()
	if p != nil {
		p.close()
	}
}
