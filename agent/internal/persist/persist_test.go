package persist

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// isolate points UserConfigDir / UserHomeDir at a temp dir so the test never
// touches the real autostart locations.
func isolate(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", filepath.Join(tmp, "Roaming"))
	case "darwin":
		t.Setenv("HOME", tmp)
	default:
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
		t.Setenv("HOME", tmp)
	}
	return tmp
}

func TestArmLoadDisarm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("avoids writing to the real HKCU Run key")
	}
	isolate(t)
	info := Info{ServerURL: "https://support.example.com", ResumeToken: "tok", OrgName: "Acme",
		Expires: time.Now().Add(time.Hour).Round(time.Second)}
	if err := Arm(info); err != nil {
		t.Fatal(err)
	}
	if !Armed() {
		t.Fatal("not armed after Arm")
	}
	got, ok := Load()
	if !ok || got.ResumeToken != "tok" || got.ServerURL != info.ServerURL || !got.Expires.Equal(info.Expires) {
		t.Fatalf("Load = %+v, %v", got, ok)
	}

	Disarm()
	if Armed() {
		t.Fatal("still armed after Disarm")
	}
}

func TestExpired(t *testing.T) {
	future := Info{Expires: time.Now().Add(time.Minute)}
	past := Info{Expires: time.Now().Add(-time.Minute)}
	if future.Expired() {
		t.Fatal("not expired")
	}
	if !past.Expired() {
		t.Fatal("expired")
	}
}

func TestArmRejectsEmpty(t *testing.T) {
	isolate(t)
	if err := Arm(Info{ServerURL: "https://x"}); err == nil {
		t.Fatal("expected error for missing token")
	}
	if Armed() {
		t.Fatal("armed despite error")
	}
}
