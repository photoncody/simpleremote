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

	consentCh chan bool
}

// New creates a session. Call Run to start it.
func New(opts Options) *Session {
	if opts.Log == nil {
		opts.Log = slog.Default()
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
	err := s.connect(true)
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

func (s *Session) reconnect() error {
	deadline := time.Now().Add(50 * time.Second)
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
		alreadyConsented := s.consented || h.Resumed
		s.consented = alreadyConsented
		s.mu.Unlock()
		_ = s.send(proto.TypeAgentInfo, proto.AgentInfo{
			Hostname: platform.Hostname(), OS: runtime.GOOS, Arch: runtime.GOARCH,
			Username: platform.Username(), Version: s.opts.Version, Elevated: platform.IsElevated(),
		})
		if alreadyConsented {
			s.opts.UI.Active(h)
			return nil
		}
		go s.awaitConsent(h)

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
		go s.handleOffer(sdp)

	case proto.TypeICE:
		var c proto.ICECandidate
		if json.Unmarshal(env.Data, &c) == nil {
			s.mu.Lock()
			p := s.rtc
			s.mu.Unlock()
			if p != nil {
				p.addCandidate(c)
			}
		}

	case proto.TypeElevateRequest:
		if platform.CanElevate() {
			s.opts.UI.ElevationRequested(s.Hello().TechName)
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
	if !allowed {
		_ = s.send(proto.TypeConsent, proto.Consent{Allowed: false})
		s.finish("You declined the connection.", false)
		return
	}
	if p := platform.CheckPermissions(); !p.OK() {
		platform.RequestPermissions()
		s.opts.UI.NeedPermissions(p)
	}
	s.mu.Lock()
	s.consented = true
	s.mu.Unlock()
	if err := s.send(proto.TypeConsent, proto.Consent{Allowed: true}); err != nil {
		s.log.Warn("send consent", "err", err)
	}
	s.opts.UI.Active(h)
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
	s.mu.Lock()
	token, server, org := s.resumeToken, s.opts.ServerURL, s.hello.OrgName
	s.mu.Unlock()
	if token == "" {
		return errors.New("not connected")
	}
	err := platform.RelaunchElevated([]string{"--server", server, "--resume", token, "--org", org})
	if err != nil {
		if errors.Is(err, platform.ErrCancelled) {
			s.DeclineElevation()
		}
		return err
	}
	s.audit("elevation_accepted", "")
	s.mu.Lock()
	s.handoff = true
	ws := s.ws
	s.mu.Unlock()
	if ws != nil {
		ws.Close(websocket.StatusNormalClosure, "relaunching elevated")
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
	s.mu.Unlock()
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
