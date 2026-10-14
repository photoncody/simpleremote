//go:build windows

package input

import (
	"errors"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procSendInput    = user32.NewProc("SendInput")
	procGetSysMetric = user32.NewProc("GetSystemMetrics")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseWheel      = 0x0800
	mouseHWheel     = 0x1000
	mouseVirtualDsk = 0x4000
	mouseAbsolute   = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyScancode = 0x0008

	smXVirtual  = 76
	smYVirtual  = 77
	smCXVirtual = 78
	smCYVirtual = 79
)

// Layouts match the 64-bit and 32-bit Windows INPUT struct (the union is
// sized by MOUSEINPUT).
type mouseInput struct {
	typ       uint32
	_         [4]byte // padding on 64-bit; harmless on 386 where we don't build
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extra     uintptr
}

type keyboardInput struct {
	typ   uint32
	_     [4]byte
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
	_     [8]byte // pad to sizeof(INPUT)
}

type winInjector struct {
	accX, accY float64
}

// New returns the Windows injector.
func New() (Injector, error) { return &winInjector{}, nil }

func sysMetric(i int) int {
	r, _, _ := procGetSysMetric.Call(uintptr(i))
	return int(int32(r))
}

func sendMouse(mi mouseInput) error {
	mi.typ = inputMouse
	n, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&mi)), unsafe.Sizeof(mi))
	if n != 1 {
		return errors.Join(errors.New("SendInput failed (is a higher-privilege window focused?)"), err)
	}
	return nil
}

func sendKey(ki keyboardInput) error {
	ki.typ = inputKeyboard
	n, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&ki)), unsafe.Sizeof(ki))
	if n != 1 {
		return errors.Join(errors.New("SendInput failed (is a higher-privilege window focused?)"), err)
	}
	return nil
}

func (w *winInjector) MoveTo(x, y int) error {
	vx, vy := sysMetric(smXVirtual), sysMetric(smYVirtual)
	vw, vh := sysMetric(smCXVirtual), sysMetric(smCYVirtual)
	if vw <= 1 || vh <= 1 {
		return errors.New("no virtual screen")
	}
	ax := int32(math.Round(float64(x-vx) * 65535 / float64(vw-1)))
	ay := int32(math.Round(float64(y-vy) * 65535 / float64(vh-1)))
	return sendMouse(mouseInput{dx: ax, dy: ay, flags: mouseMove | mouseAbsolute | mouseVirtualDsk})
}

func (w *winInjector) Button(b int, down bool) error {
	var f uint32
	switch b {
	case 0:
		f = mouseLeftUp
		if down {
			f = mouseLeftDown
		}
	case 1:
		f = mouseMiddleUp
		if down {
			f = mouseMiddleDown
		}
	case 2:
		f = mouseRightUp
		if down {
			f = mouseRightDown
		}
	default:
		return nil
	}
	return sendMouse(mouseInput{flags: f})
}

func (w *winInjector) Scroll(dx, dy float64) error {
	// WHEEL_DELTA is 120 per notch; Windows accepts partial notches.
	w.accX += dx * 120
	w.accY += dy * 120
	if v := int32(w.accY); v != 0 {
		w.accY -= float64(v)
		if err := sendMouse(mouseInput{mouseData: uint32(-v), flags: mouseWheel}); err != nil {
			return err
		}
	}
	if v := int32(w.accX); v != 0 {
		w.accX -= float64(v)
		return sendMouse(mouseInput{mouseData: uint32(v), flags: mouseHWheel})
	}
	return nil
}

func (w *winInjector) Key(k Key, down bool) error {
	var ki keyboardInput
	if k.Win != 0 {
		ki.scan = k.Win & 0xFF
		ki.flags = keyScancode
		if k.Win&0xFF00 == 0xE000 {
			ki.flags |= keyExtended
		}
	} else if k.WinVK != 0 {
		ki.vk = k.WinVK
	} else {
		return nil
	}
	if !down {
		ki.flags |= keyUp
	}
	return sendKey(ki)
}

func (w *winInjector) Close() error { return nil }
