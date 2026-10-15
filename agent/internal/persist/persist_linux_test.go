//go:build linux

package persist

import (
	"os"
	"testing"
	"time"
)

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
