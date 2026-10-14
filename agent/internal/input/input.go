// Package input injects the tech's pointer and keyboard events into the
// local desktop.
package input

import (
	"image"
	"math"
)

// Injector is implemented per operating system.
type Injector interface {
	// MoveTo moves the pointer to an absolute position in the platform's
	// global desktop coordinates (the same space as screen.Bounds).
	MoveTo(x, y int) error
	// Button presses (down) or releases a mouse button: 0 left, 1 middle, 2 right.
	Button(button int, down bool) error
	// Scroll scrolls by a number of "notches" (positive dy = down, positive
	// dx = right). Fractions accumulate.
	Scroll(dx, dy float64) error
	// Key presses or releases a key.
	Key(k Key, down bool) error
	// Close releases any resources.
	Close() error
}

// Key identifies a physical key for each platform.
type Key struct {
	Code   string // browser KeyboardEvent.code
	Win    uint16 // PC set-1 scancode; 0xE0xx for extended keys
	WinVK  uint16 // Windows virtual key, used when Win is 0
	Evdev  uint16 // Linux input event code (X keycode = Evdev + 8)
	Mac    uint16 // macOS virtual keycode
	HasMac bool
}

// Lookup finds the key for a KeyboardEvent.code.
func Lookup(code string) (Key, bool) {
	k, ok := keys[code]
	return k, ok
}

// Controller maps normalized pointer coordinates onto a display and tracks
// held buttons and keys so they can be released when the session ends.
type Controller struct {
	inj     Injector
	display image.Rectangle
	held    map[string]Key
	buttons map[int]bool
	accX    float64
	accY    float64
}

// NewController wraps an injector.
func NewController(inj Injector) *Controller {
	return &Controller{inj: inj, held: map[string]Key{}, buttons: map[int]bool{}}
}

// SetDisplay sets the rectangle that normalized coordinates refer to.
func (c *Controller) SetDisplay(r image.Rectangle) { c.display = r }

// Move moves the pointer to (nx, ny) in 0..1 display coordinates.
func (c *Controller) Move(nx, ny float64) error {
	if c.display.Empty() {
		return nil
	}
	nx = math.Min(1, math.Max(0, nx))
	ny = math.Min(1, math.Max(0, ny))
	x := c.display.Min.X + int(math.Round(nx*float64(c.display.Dx()-1)))
	y := c.display.Min.Y + int(math.Round(ny*float64(c.display.Dy()-1)))
	return c.inj.MoveTo(x, y)
}

// Button presses or releases a mouse button.
func (c *Controller) Button(b int, down bool) error {
	if b < 0 || b > 2 {
		return nil
	}
	if !down && !c.buttons[b] {
		return nil
	}
	c.buttons[b] = down
	return c.inj.Button(b, down)
}

// Wheel scrolls by browser wheel deltas (CSS pixels). About 100 pixels make
// one notch.
func (c *Controller) Wheel(dx, dy float64) error {
	c.accX += dx / 100
	c.accY += dy / 100
	sx, sy := c.accX, c.accY
	c.accX, c.accY = 0, 0
	if sx == 0 && sy == 0 {
		return nil
	}
	return c.inj.Scroll(sx, sy)
}

// Key presses or releases the key with the given KeyboardEvent.code.
func (c *Controller) Key(code string, down bool) error {
	k, ok := Lookup(code)
	if !ok {
		return nil
	}
	if down {
		c.held[code] = k
	} else {
		if _, ok := c.held[code]; !ok {
			return nil
		}
		delete(c.held, code)
	}
	return c.inj.Key(k, down)
}

// ReleaseAll lets go of every held key and button.
func (c *Controller) ReleaseAll() {
	for code, k := range c.held {
		_ = c.inj.Key(k, false)
		delete(c.held, code)
	}
	for b, down := range c.buttons {
		if down {
			_ = c.inj.Button(b, false)
		}
		delete(c.buttons, b)
	}
}

// Close releases everything and closes the injector.
func (c *Controller) Close() error {
	c.ReleaseAll()
	return c.inj.Close()
}
