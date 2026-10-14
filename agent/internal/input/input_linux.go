//go:build linux

package input

import (
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// X11 injector using the XTEST extension. Wayland sessions are not
// supported yet (they need the xdg-desktop-portal RemoteDesktop API).
type x11Injector struct {
	conn       *xgb.Conn
	root       xproto.Window
	accX, accY float64
}

// New connects to the X server named by $DISPLAY.
func New() (Injector, error) {
	if os.Getenv("DISPLAY") == "" {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return nil, errors.New("Wayland sessions are not supported yet; log in with an X11 (Xorg) session")
		}
		return nil, errors.New("no X display found ($DISPLAY is not set)")
	}
	c, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to X server: %w", err)
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return nil, fmt.Errorf("X server has no XTEST extension: %w", err)
	}
	root := xproto.Setup(c).DefaultScreen(c).Root
	return &x11Injector{conn: c, root: root}, nil
}

func (x *x11Injector) fake(typ byte, detail byte, px, py int16) error {
	return xtest.FakeInputChecked(x.conn, typ, detail, 0, x.root, px, py, 0).Check()
}

func (x *x11Injector) MoveTo(px, py int) error {
	return x.fake(xproto.MotionNotify, 0, int16(px), int16(py))
}

func (x *x11Injector) Button(b int, down bool) error {
	btn := map[int]byte{0: 1, 1: 2, 2: 3}[b]
	if btn == 0 {
		return nil
	}
	typ := byte(xproto.ButtonRelease)
	if down {
		typ = xproto.ButtonPress
	}
	return x.fake(typ, btn, 0, 0)
}

func (x *x11Injector) click(btn byte, n int) error {
	for i := 0; i < n; i++ {
		if err := x.fake(xproto.ButtonPress, btn, 0, 0); err != nil {
			return err
		}
		if err := x.fake(xproto.ButtonRelease, btn, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func (x *x11Injector) Scroll(dx, dy float64) error {
	// X11 scrolls in whole notches: buttons 4/5 (vertical) and 6/7 (horizontal).
	x.accX += dx
	x.accY += dy
	if n := int(math.Trunc(x.accY)); n != 0 {
		x.accY -= float64(n)
		btn := byte(5)
		if n < 0 {
			btn, n = 4, -n
		}
		if err := x.click(btn, n); err != nil {
			return err
		}
	}
	if n := int(math.Trunc(x.accX)); n != 0 {
		x.accX -= float64(n)
		btn := byte(7)
		if n < 0 {
			btn, n = 6, -n
		}
		return x.click(btn, n)
	}
	return nil
}

func (x *x11Injector) Key(k Key, down bool) error {
	if k.Evdev == 0 {
		return nil
	}
	typ := byte(xproto.KeyRelease)
	if down {
		typ = xproto.KeyPress
	}
	// With the standard evdev keymap, X keycodes are evdev codes plus 8.
	return x.fake(typ, byte(k.Evdev+8), 0, 0)
}

func (x *x11Injector) Close() error {
	x.conn.Close()
	return nil
}
