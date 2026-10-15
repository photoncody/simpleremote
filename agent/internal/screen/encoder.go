// Package screen captures displays and encodes changed regions as JPEG tiles.
package screen

import (
	"bytes"
	"image"
	"image/jpeg"
	"runtime"
	"sync"

	"github.com/photoncody/simpleremote/internal/proto"
)

// TileSize is the edge length of the square tiles the screen is split into.
const TileSize = 128

// Encoder turns successive frames into tile messages, sending only tiles
// that changed since the previous frame.
type Encoder struct {
	Quality int

	prev *image.RGBA
}

// Reset forces the next frame to be sent in full.
func (e *Encoder) Reset() { e.prev = nil }

type tileJob struct {
	rect image.Rectangle
	out  []byte
	err  error
}

// Encode returns the tile messages for img followed by a frame-end marker.
// It returns nil if nothing changed. img must not be modified afterwards;
// the encoder keeps it to diff the next frame against.
func (e *Encoder) Encode(img *image.RGBA, seq uint32) ([][]byte, error) {
	b := img.Bounds()
	full := e.prev == nil || e.prev.Bounds() != b

	var jobs []*tileJob
	for y := b.Min.Y; y < b.Max.Y; y += TileSize {
		for x := b.Min.X; x < b.Max.X; x += TileSize {
			r := image.Rect(x, y, min(x+TileSize, b.Max.X), min(y+TileSize, b.Max.Y))
			if full || changed(e.prev, img, r) {
				jobs = append(jobs, &tileJob{rect: r})
			}
		}
	}
	e.prev = img
	if len(jobs) == 0 {
		return nil, nil
	}

	q := e.Quality
	if q <= 0 || q > 100 {
		q = 65
	}
	workers := min(runtime.GOMAXPROCS(0), len(jobs))
	ch := make(chan *tileJob)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			for j := range ch {
				buf.Reset()
				j.err = jpeg.Encode(&buf, img.SubImage(j.rect), &jpeg.Options{Quality: q})
				j.out = proto.AppendTile(make([]byte, 0, buf.Len()+32), proto.TileHeader{
					Seq: seq,
					X:   uint16(j.rect.Min.X - b.Min.X), Y: uint16(j.rect.Min.Y - b.Min.Y),
					W: uint16(j.rect.Dx()), H: uint16(j.rect.Dy()),
					ScreenW: uint16(b.Dx()), ScreenH: uint16(b.Dy()),
				}, buf.Bytes())
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	out := make([][]byte, 0, len(jobs)+1)
	for _, j := range jobs {
		if j.err != nil {
			e.prev = nil
			return nil, j.err
		}
		out = append(out, j.out)
	}
	return append(out, proto.FrameEnd(seq)), nil
}

// changed reports whether rectangle r differs between a and b.
func changed(a, b *image.RGBA, r image.Rectangle) bool {
	rowBytes := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		ai := a.PixOffset(r.Min.X, y)
		bi := b.PixOffset(r.Min.X, y)
		if !bytes.Equal(a.Pix[ai:ai+rowBytes], b.Pix[bi:bi+rowBytes]) {
			return true
		}
	}
	return false
}
