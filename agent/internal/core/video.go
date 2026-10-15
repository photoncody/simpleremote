package core

import (
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/photoncody/simpleremote/agent/internal/screen"
	"github.com/photoncody/simpleremote/internal/proto"
)

const (
	frameInterval    = time.Second / 15
	maxFramesAhead   = 2       // frames sent but not yet acknowledged
	maxBufferedVideo = 4 << 20 // bytes queued on the channel
)

// videoLoop captures the selected display and streams changed tiles until
// the channel or peer closes.
func (p *peer) videoLoop(dc *webrtc.DataChannel) {
	var enc screen.Encoder
	var seq uint32
	var lastErr string
	t := time.NewTicker(frameInterval)
	defer t.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-t.C:
		}
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			if dc.ReadyState() == webrtc.DataChannelStateClosed {
				return
			}
			continue
		}
		if seq-p.acked.Load() >= maxFramesAhead || dc.BufferedAmount() > maxBufferedVideo {
			continue
		}

		p.mu.Lock()
		disp, q, refresh := p.display, p.quality, p.refresh
		p.refresh = false
		p.mu.Unlock()
		if refresh {
			enc.Reset()
		}
		enc.Quality = q

		img, err := screen.Capture(disp)
		if err != nil {
			if err.Error() != lastErr {
				lastErr = err.Error()
				p.s.log.Warn("screen capture failed", "err", err)
				p.sendControl(proto.Control{T: proto.CtlNotice, Text: "Screen capture failed: " + err.Error()})
			}
			continue
		}
		lastErr = ""
		msgs, err := enc.Encode(img, seq+1)
		if err != nil {
			p.s.log.Warn("encode frame", "err", err)
			continue
		}
		if msgs == nil {
			continue
		}
		seq++
		for _, m := range msgs {
			if err := dc.Send(m); err != nil {
				return
			}
		}
	}
}
