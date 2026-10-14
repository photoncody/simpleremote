package core

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/photoncody/simpleremote/agent/internal/clip"
	"github.com/photoncody/simpleremote/agent/internal/files"
	"github.com/photoncody/simpleremote/agent/internal/input"
	"github.com/photoncody/simpleremote/agent/internal/platform"
	"github.com/photoncody/simpleremote/agent/internal/screen"
	"github.com/photoncody/simpleremote/internal/proto"
)

// peer is one WebRTC connection with the tech's browser.
type peer struct {
	s      *Session
	pc     *webrtc.PeerConnection
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	control *webrtc.DataChannel
	display int
	quality int
	refresh bool
	acked   atomic.Uint32

	inputMu  sync.Mutex
	input    *input.Controller
	inputErr error

	files *files.Handler

	clipMu     sync.Mutex
	clipCancel context.CancelFunc
	clipLast   string
}

func iceServers(h proto.Hello) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(h.ICEServers))
	for _, s := range h.ICEServers {
		out = append(out, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	return out
}

func (s *Session) handleOffer(sdp proto.SDP) {
	// A new offer means the tech (re)opened the viewer; replace any old
	// connection.
	s.mu.Lock()
	old := s.rtc
	s.rtc = nil
	h := s.hello
	s.mu.Unlock()
	if old != nil {
		old.close()
	}

	p, err := newPeer(s, h)
	if err != nil {
		s.log.Error("create peer connection", "err", err)
		return
	}
	s.mu.Lock()
	s.rtc = p
	s.mu.Unlock()

	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp.SDP}); err != nil {
		s.log.Error("set remote description", "err", err)
		return
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		s.log.Error("create answer", "err", err)
		return
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		s.log.Error("set local description", "err", err)
		return
	}
	_ = s.send(proto.TypeAnswer, proto.SDP{Type: "answer", SDP: answer.SDP})
}

func newPeer(s *Session, h proto.Hello) (*peer, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers(h)})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	p := &peer{s: s, pc: pc, ctx: ctx, cancel: cancel, quality: 65, refresh: true}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		j := c.ToJSON()
		_ = s.send(proto.TypeICE, proto.ICECandidate{Candidate: j.Candidate, SDPMid: j.SDPMid,
			SDPMLineIndex: j.SDPMLineIndex, UsernameFragment: j.UsernameFragment})
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.log.Info("peer connection", "state", st.String())
		if st == webrtc.PeerConnectionStateConnected {
			s.opts.UI.Activity(fmt.Sprintf("%s is connected.", s.Hello().TechName))
		}
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case proto.ChannelVideo:
			dc.OnOpen(func() { go p.videoLoop(dc) })
		case proto.ChannelInput:
			dc.OnMessage(func(m webrtc.DataChannelMessage) { p.onInput(m.Data) })
		case proto.ChannelControl:
			p.mu.Lock()
			p.control = dc
			p.mu.Unlock()
			dc.OnOpen(func() { p.sendInitialState() })
			dc.OnMessage(func(m webrtc.DataChannelMessage) { p.onControl(m.Data) })
		case proto.ChannelFiles:
			fh := files.New(&fileChannel{dc: dc}, files.DefaultSaveDir(), p.onFileNotice)
			p.mu.Lock()
			p.files = fh
			p.mu.Unlock()
			dc.OnMessage(func(m webrtc.DataChannelMessage) {
				if m.IsString {
					fh.OnText(m.Data)
				} else {
					fh.OnBinary(m.Data)
				}
			})
		default:
			dc.Close()
		}
	})
	return p, nil
}

func (p *peer) addCandidate(c proto.ICECandidate) {
	err := p.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid,
		SDPMLineIndex: c.SDPMLineIndex, UsernameFragment: c.UsernameFragment})
	if err != nil {
		p.s.log.Debug("add ICE candidate", "err", err)
	}
}

func (p *peer) close() {
	p.cancel()
	p.inputMu.Lock()
	if p.input != nil {
		p.input.Close()
		p.input = nil
	}
	p.inputMu.Unlock()
	p.mu.Lock()
	fh := p.files
	p.mu.Unlock()
	if fh != nil {
		fh.Close()
	}
	p.stopClipboard()
	_ = p.pc.Close()
}

// ---- control ----

func (p *peer) sendControl(c proto.Control) bool {
	p.mu.Lock()
	dc := p.control
	p.mu.Unlock()
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return false
	}
	b, _ := json.Marshal(c)
	return dc.SendText(string(b)) == nil
}

func (p *peer) sendInitialState() {
	p.mu.Lock()
	cur := p.display
	p.mu.Unlock()
	p.sendControl(proto.Control{T: proto.CtlDisplays, Displays: screen.Displays(), Current: cur})
	p.sendControl(proto.Control{T: proto.CtlState, Elevated: platform.IsElevated()})
	if perms := platform.CheckPermissions(); !perms.OK() {
		p.sendControl(proto.Control{T: proto.CtlNotice,
			Text: "The user hasn't granted Screen Recording and Accessibility permissions yet. Ask them to allow SimpleRemote in System Settings and reopen it."})
	}
}

func (p *peer) onControl(data []byte) {
	var c proto.Control
	if json.Unmarshal(data, &c) != nil {
		return
	}
	switch c.T {
	case proto.CtlAck:
		p.acked.Store(c.Seq)
	case proto.CtlRefresh:
		p.mu.Lock()
		p.refresh = true
		p.mu.Unlock()
	case proto.CtlDisplay:
		if _, err := screen.Bounds(c.ID); err != nil {
			return
		}
		p.mu.Lock()
		p.display, p.refresh = c.ID, true
		p.mu.Unlock()
		p.sendControl(proto.Control{T: proto.CtlDisplays, Displays: screen.Displays(), Current: c.ID})
	case proto.CtlQuality:
		if c.Quality >= 10 && c.Quality <= 95 {
			p.mu.Lock()
			p.quality, p.refresh = c.Quality, true
			p.mu.Unlock()
		}
	case proto.CtlChat:
		if c.Text != "" {
			p.s.opts.UI.Chat(p.s.Hello().TechName, c.Text)
		}
	case proto.CtlClip:
		p.writeClipboard(c.Text)
	case proto.CtlClipSync:
		if c.On {
			p.startClipboard()
		} else {
			p.stopClipboard()
		}
	}
}

// ---- input ----

func (p *peer) controller() (*input.Controller, error) {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()
	if p.input != nil || p.inputErr != nil {
		return p.input, p.inputErr
	}
	inj, err := input.New()
	if err != nil {
		p.inputErr = err
		p.sendControl(proto.Control{T: proto.CtlNotice, Text: "Remote control is unavailable: " + err.Error()})
		return nil, err
	}
	p.input = input.NewController(inj)
	return p.input, nil
}

func (p *peer) onInput(data []byte) {
	var ev proto.Input
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	c, err := p.controller()
	if err != nil {
		return
	}
	p.mu.Lock()
	disp := p.display
	p.mu.Unlock()
	if b, err := screen.Bounds(disp); err == nil {
		c.SetDisplay(b)
	} else {
		c.SetDisplay(image.Rectangle{})
	}
	switch ev.T {
	case proto.InputMouseMove:
		err = c.Move(ev.X, ev.Y)
	case proto.InputMouseDown, proto.InputMouseUp:
		err = c.Button(ev.B, ev.T == proto.InputMouseDown)
	case proto.InputWheel:
		err = c.Wheel(ev.DX, ev.DY)
	case proto.InputKeyDown, proto.InputKeyUp:
		err = c.Key(ev.C, ev.T == proto.InputKeyDown)
	}
	if err != nil {
		p.s.log.Debug("inject input", "type", ev.T, "err", err)
	}
}

// ---- clipboard ----

func (p *peer) writeClipboard(text string) {
	if err := clip.Init(); err != nil {
		p.sendControl(proto.Control{T: proto.CtlNotice, Text: "The clipboard is unavailable: " + err.Error()})
		return
	}
	p.clipMu.Lock()
	p.clipLast = text
	p.clipMu.Unlock()
	clip.Write(text)
}

func (p *peer) startClipboard() {
	if err := clip.Init(); err != nil {
		p.sendControl(proto.Control{T: proto.CtlNotice, Text: "The clipboard is unavailable: " + err.Error()})
		return
	}
	p.clipMu.Lock()
	if p.clipCancel != nil {
		p.clipMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	p.clipCancel = cancel
	p.clipMu.Unlock()
	p.s.opts.UI.Activity("Clipboard sharing turned on: text you copy is sent to the technician.")
	p.s.audit("clipboard_sync_on", "")
	go func() {
		for text := range clip.Watch(ctx) {
			p.clipMu.Lock()
			echo := text == p.clipLast
			p.clipLast = text
			p.clipMu.Unlock()
			if !echo && len(text) <= 1<<20 {
				p.sendControl(proto.Control{T: proto.CtlClip, Text: text})
			}
		}
	}()
}

func (p *peer) stopClipboard() {
	p.clipMu.Lock()
	cancel := p.clipCancel
	p.clipCancel = nil
	p.clipMu.Unlock()
	if cancel != nil {
		cancel()
		if p.ctx.Err() == nil {
			p.s.opts.UI.Activity("Clipboard sharing turned off.")
			p.s.audit("clipboard_sync_off", "")
		}
	}
}

// ---- files ----

func (p *peer) onFileNotice(n files.Notice) {
	switch {
	case n.Err != nil:
		p.s.opts.UI.Activity(fmt.Sprintf("A file transfer failed (%s): %v", n.Name, n.Err))
	case n.Direction == "received":
		p.s.opts.UI.Activity(fmt.Sprintf("The technician sent you %s (saved in %s).", n.Name, n.Path))
	default:
		p.s.opts.UI.Activity(fmt.Sprintf("The technician copied %s from your computer.", n.Path))
	}
}

type fileChannel struct{ dc *webrtc.DataChannel }

func (f *fileChannel) SendText(s string) error { return f.dc.SendText(s) }
func (f *fileChannel) Send(b []byte) error     { return f.dc.Send(b) }

func (f *fileChannel) WaitBuffered(ctx context.Context) error {
	for f.dc.BufferedAmount() > 2<<20 {
		if f.dc.ReadyState() != webrtc.DataChannelStateOpen {
			return fmt.Errorf("channel closed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}
