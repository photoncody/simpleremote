//go:build darwin

package persist

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const launchLabel = "com.github.photoncody.simpleremote"

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist"), nil
}

// enableAutostart writes a per-user LaunchAgent that reopens the app bundle at
// login. Reopening the bundle (rather than the inner binary) keeps the app's
// identity, so a granted Screen Recording permission still applies.
func enableAutostart(exe string) error {
	args := []string{exe, "--from-autostart"}
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		bundle := exe[:i+len(".app")]
		args = []string{"/usr/bin/open", "-n", bundle, "--args", "--from-autostart"}
	}
	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0"><dict>` + "\n")
	b.WriteString("<key>Label</key><string>" + launchLabel + "</string>\n")
	b.WriteString("<key>ProgramArguments</key><array>\n")
	for _, a := range args {
		b.WriteString("<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString("</array>\n")
	// Run once at login, not kept alive: this is a one-shot reconnect.
	b.WriteString("<key>RunAtLoad</key><true/>\n")
	b.WriteString("</dict></plist>\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "load", "-w", path).Run()
	return nil
}

func disableAutostart() {
	path, err := plistPath()
	if err != nil {
		return
	}
	_ = exec.Command("launchctl", "unload", "-w", path).Run()
	os.Remove(path)
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
