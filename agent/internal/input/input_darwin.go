//go:build darwin

package input

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

static void postMouse(CGEventType type, double x, double y, CGMouseButton button, int clicks, CGEventFlags flags) {
	CGEventRef ev = CGEventCreateMouseEvent(NULL, type, CGPointMake(x, y), button);
	if (!ev) return;
	if (clicks > 0) CGEventSetIntegerValueField(ev, kCGMouseEventClickState, clicks);
	CGEventSetFlags(ev, flags);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}

static void postScroll(int32_t dy, int32_t dx) {
	CGEventRef ev = CGEventCreateScrollWheelEvent2(NULL, kCGScrollEventUnitPixel, 2, dy, dx, 0);
	if (!ev) return;
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}

static void postKey(CGKeyCode code, bool down, CGEventFlags flags) {
	CGEventRef ev = CGEventCreateKeyboardEvent(NULL, code, down);
	if (!ev) return;
	CGEventSetFlags(ev, flags);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}
*/
import "C"

import (
	"math"
	"time"
)

type macInjector struct {
	x, y       float64
	buttons    [3]bool
	flags      C.CGEventFlags
	lastDown   time.Time
	lastBtn    int
	lastPos    [2]float64
	clicks     int
	accX, accY float64
}

// New returns the macOS injector. The app needs the Accessibility
// permission for events to take effect.
func New() (Injector, error) { return &macInjector{}, nil }

var modifierFlags = map[uint16]C.CGEventFlags{
	0x38: C.kCGEventFlagMaskShift, 0x3C: C.kCGEventFlagMaskShift,
	0x3B: C.kCGEventFlagMaskControl, 0x3E: C.kCGEventFlagMaskControl,
	0x3A: C.kCGEventFlagMaskAlternate, 0x3D: C.kCGEventFlagMaskAlternate,
	0x37: C.kCGEventFlagMaskCommand, 0x36: C.kCGEventFlagMaskCommand,
}

func (m *macInjector) MoveTo(x, y int) error {
	m.x, m.y = float64(x), float64(y)
	typ, btn := C.CGEventType(C.kCGEventMouseMoved), C.CGMouseButton(C.kCGMouseButtonLeft)
	switch {
	case m.buttons[0]:
		typ = C.kCGEventLeftMouseDragged
	case m.buttons[2]:
		typ, btn = C.kCGEventRightMouseDragged, C.kCGMouseButtonRight
	case m.buttons[1]:
		typ, btn = C.kCGEventOtherMouseDragged, C.kCGMouseButtonCenter
	}
	C.postMouse(typ, C.double(m.x), C.double(m.y), btn, 0, m.flags)
	return nil
}

func (m *macInjector) Button(b int, down bool) error {
	if b < 0 || b > 2 {
		return nil
	}
	m.buttons[b] = down
	if down {
		// macOS needs an explicit click count for double-clicks.
		now := time.Now()
		near := math.Abs(m.x-m.lastPos[0]) < 5 && math.Abs(m.y-m.lastPos[1]) < 5
		if b == m.lastBtn && near && now.Sub(m.lastDown) < 500*time.Millisecond {
			m.clicks++
		} else {
			m.clicks = 1
		}
		m.lastDown, m.lastBtn, m.lastPos = now, b, [2]float64{m.x, m.y}
	}
	var typ C.CGEventType
	var btn C.CGMouseButton
	switch b {
	case 0:
		typ, btn = C.kCGEventLeftMouseUp, C.kCGMouseButtonLeft
		if down {
			typ = C.kCGEventLeftMouseDown
		}
	case 1:
		typ, btn = C.kCGEventOtherMouseUp, C.kCGMouseButtonCenter
		if down {
			typ = C.kCGEventOtherMouseDown
		}
	case 2:
		typ, btn = C.kCGEventRightMouseUp, C.kCGMouseButtonRight
		if down {
			typ = C.kCGEventRightMouseDown
		}
	}
	C.postMouse(typ, C.double(m.x), C.double(m.y), btn, C.int(m.clicks), m.flags)
	return nil
}

func (m *macInjector) Scroll(dx, dy float64) error {
	m.accX += dx * 40
	m.accY += dy * 40
	ix, iy := int32(m.accX), int32(m.accY)
	m.accX -= float64(ix)
	m.accY -= float64(iy)
	if ix != 0 || iy != 0 {
		C.postScroll(C.int32_t(-iy), C.int32_t(-ix))
	}
	return nil
}

func (m *macInjector) Key(k Key, down bool) error {
	if !k.HasMac {
		return nil
	}
	if f, ok := modifierFlags[k.Mac]; ok {
		if down {
			m.flags |= f
		} else {
			m.flags &^= f
		}
	}
	C.postKey(C.CGKeyCode(k.Mac), C.bool(down), m.flags)
	return nil
}

func (m *macInjector) Close() error { return nil }
