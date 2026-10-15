package input

import (
	"image"
	"testing"
)

type call struct {
	op   string
	a, b int
	down bool
	fx   float64
	fy   float64
}

type fake struct{ calls []call }

func (f *fake) MoveTo(x, y int) error {
	f.calls = append(f.calls, call{op: "move", a: x, b: y})
	return nil
}
func (f *fake) Button(b int, d bool) error {
	f.calls = append(f.calls, call{op: "button", a: b, down: d})
	return nil
}
func (f *fake) Scroll(dx, dy float64) error {
	f.calls = append(f.calls, call{op: "scroll", fx: dx, fy: dy})
	return nil
}
func (f *fake) Key(k Key, d bool) error {
	f.calls = append(f.calls, call{op: "key:" + k.Code, down: d})
	return nil
}
func (f *fake) Close() error { return nil }

func TestMoveMapsToDisplay(t *testing.T) {
	f := &fake{}
	c := NewController(f)
	c.SetDisplay(image.Rect(-1920, 0, 0, 1080)) // monitor left of primary
	_ = c.Move(0, 0)
	_ = c.Move(1, 1)
	_ = c.Move(0.5, 2) // clamped
	want := []call{{op: "move", a: -1920, b: 0}, {op: "move", a: -1, b: 1079}, {op: "move", a: -960, b: 1079}}
	for i, w := range want {
		if f.calls[i] != w {
			t.Errorf("call %d = %+v, want %+v", i, f.calls[i], w)
		}
	}
}

func TestReleaseAll(t *testing.T) {
	f := &fake{}
	c := NewController(f)
	_ = c.Key("ControlLeft", true)
	_ = c.Key("KeyC", true)
	_ = c.Key("KeyC", false)
	_ = c.Button(0, true)
	_ = c.Key("NotAKey", true)
	_ = c.Key("ShiftLeft", false) // never pressed: ignored
	f.calls = nil
	c.ReleaseAll()
	if len(f.calls) != 2 {
		t.Fatalf("ReleaseAll calls = %+v", f.calls)
	}
}

func TestKeymapComplete(t *testing.T) {
	for _, code := range []string{"KeyA", "Digit0", "Enter", "ArrowLeft", "MetaLeft", "F12", "NumpadEnter", "Pause"} {
		k, ok := Lookup(code)
		if !ok || (k.Win == 0 && k.WinVK == 0) || k.Evdev == 0 {
			t.Errorf("%s: %+v %v", code, k, ok)
		}
	}
	seenWin := map[uint16]string{}
	for code, k := range keys {
		if code == "OSLeft" || code == "OSRight" || k.Win == 0 {
			continue
		}
		if other, dup := seenWin[k.Win]; dup {
			t.Errorf("scancode %#x used by %s and %s", k.Win, code, other)
		}
		seenWin[k.Win] = code
	}
}
