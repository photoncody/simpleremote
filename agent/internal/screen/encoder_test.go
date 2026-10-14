package screen

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/photoncody/simpleremote/internal/proto"
)

func frame(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

func TestEncoderSendsOnlyChangedTiles(t *testing.T) {
	var e Encoder
	// 300x200 => 3x2 tiles (the last column and row are partial).
	f1 := frame(300, 200, color.RGBA{10, 20, 30, 255})
	msgs, err := e.Encode(f1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 6+1 {
		t.Fatalf("first frame: %d messages, want 7", len(msgs))
	}
	if msgs[len(msgs)-1][0] != proto.VideoFrameEnd {
		t.Fatal("last message should be frame end")
	}
	h, data, err := proto.ParseTile(msgs[5])
	if err != nil {
		t.Fatal(err)
	}
	if h.X != 256 || h.Y != 128 || h.W != 44 || h.H != 72 || h.ScreenW != 300 || h.ScreenH != 200 {
		t.Fatalf("last tile header = %+v", h)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("tile is not a JPEG: %v", err)
	}

	// Same picture: nothing to send.
	f2 := frame(300, 200, color.RGBA{10, 20, 30, 255})
	if msgs, _ := e.Encode(f2, 2); msgs != nil {
		t.Fatalf("unchanged frame sent %d messages", len(msgs))
	}

	// Change one pixel in the middle tile of the top row.
	f3 := frame(300, 200, color.RGBA{10, 20, 30, 255})
	f3.SetRGBA(130, 5, color.RGBA{255, 0, 0, 255})
	msgs, _ = e.Encode(f3, 3)
	if len(msgs) != 2 {
		t.Fatalf("one changed pixel sent %d messages, want 2", len(msgs))
	}
	h, _, _ = proto.ParseTile(msgs[0])
	if h.X != 128 || h.Y != 0 || h.Seq != 3 {
		t.Fatalf("changed tile = %+v", h)
	}

	// Reset forces a full frame.
	e.Reset()
	if msgs, _ := e.Encode(f3, 4); len(msgs) != 7 {
		t.Fatalf("after reset: %d messages", len(msgs))
	}
}

func TestEncoderResolutionChange(t *testing.T) {
	var e Encoder
	_, _ = e.Encode(frame(256, 256, color.RGBA{}), 1)
	msgs, _ := e.Encode(frame(128, 128, color.RGBA{}), 2)
	if len(msgs) != 2 {
		t.Fatalf("resolution change sent %d messages, want full frame (2)", len(msgs))
	}
}

func BenchmarkEncode1080p(b *testing.B) {
	var e Encoder
	img := frame(1920, 1080, color.RGBA{40, 80, 120, 255})
	for i := 0; i < b.N; i++ {
		e.Reset()
		if _, err := e.Encode(img, uint32(i)); err != nil {
			b.Fatal(err)
		}
	}
}
