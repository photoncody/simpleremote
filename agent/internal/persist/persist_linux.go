//go:build linux

package persist

import (
	"os"
	"path/filepath"
	"strings"
)

// XDG autostart: a .desktop file in ~/.config/autostart runs once at the next
// desktop login. Per-user, no root, honored by GNOME, KDE, XFCE and others.
func desktopPath() (string, error) {
	c, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "autostart", "simpleremote.desktop"), nil
}

func enableAutostart(exe string) error {
	path, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content := strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=SimpleRemote",
		"Comment=Reconnect to an active remote support session after restart",
		"Exec=" + quote(exe) + " --from-autostart",
		"Terminal=false",
		"X-GNOME-Autostart-enabled=true",
		"",
	}, "\n")
	return os.WriteFile(path, []byte(content), 0o600)
}

func disableAutostart() {
	if path, err := desktopPath(); err == nil {
		os.Remove(path)
	}
}

// quote wraps a path in double quotes for the .desktop Exec line, escaping
// backslashes and quotes as the spec requires.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
