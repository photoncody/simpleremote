package screen

import (
	"fmt"
	"image"

	"github.com/kbinani/screenshot"

	"github.com/photoncody/simpleremote/internal/proto"
)

// Displays lists the active monitors. Bounds are in the coordinate space the
// input package uses for the same platform.
func Displays() []proto.Display {
	n := screenshot.NumActiveDisplays()
	out := make([]proto.Display, 0, n)
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		out = append(out, proto.Display{
			ID: i, Name: fmt.Sprintf("Display %d", i+1),
			X: b.Min.X, Y: b.Min.Y, Width: b.Dx(), Height: b.Dy(),
			Primary: i == 0,
		})
	}
	return out
}

// Bounds returns the bounds of display id.
func Bounds(id int) (image.Rectangle, error) {
	if id < 0 || id >= screenshot.NumActiveDisplays() {
		return image.Rectangle{}, fmt.Errorf("display %d not found", id)
	}
	return screenshot.GetDisplayBounds(id), nil
}

// Capture grabs display id.
func Capture(id int) (*image.RGBA, error) {
	b, err := Bounds(id)
	if err != nil {
		return nil, err
	}
	img, err := screenshot.CaptureRect(b)
	if err != nil {
		return nil, err
	}
	// Normalize the origin so tiles are relative to the display.
	if img.Rect.Min != (image.Point{}) {
		img.Rect = img.Rect.Sub(img.Rect.Min)
	}
	return img, nil
}
