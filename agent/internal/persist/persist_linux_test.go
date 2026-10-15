//go:build linux

package persist

import (
	"os"
	"testing"
	"time"
)

func TestQuoteDesktopExec(t *testing.T) {
	cases := map[string]string{
		"/tmp/SimpleRemote-123456":   `"/tmp/SimpleRemote-123456"`,
		"/home/a b/SimpleRemote (1)": `"/home/a b/SimpleRemote (1)"`,
		`/tmp/$HOME`:                 `"/tmp/\\$HOME"`,
		"/tmp/`id`":                  "\"/tmp/\\\\`id\\\\`\"",
		`/tmp/50%`:                   `"/tmp/50%%"`,
		`/tmp/q"x`:                   `"/tmp/q\\"x"`,
		`/tmp/back\slash`:            `"/tmp/back\\\\slash"`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// Arm installs an XDG autostart entry, and Disarm removes it.
func TestLinuxAutostartFile(t *testing.T) {
	isolate(t)
	if err := Arm(Info{ServerURL: "https://x", ResumeToken: "tok", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d, err := desktopPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d); err != nil {
		t.Fatalf("autostart file missing: %v", err)
	}
	Disarm()
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Fatalf("autostart file left behind: %v", err)
	}
}
