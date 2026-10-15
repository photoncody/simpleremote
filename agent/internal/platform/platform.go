// Package platform wraps OS-specific details: elevation, DPI awareness, and
// macOS privacy permissions.
package platform

import (
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// Username returns the logged-in user's name.
func Username() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER") + os.Getenv("USERNAME")
}

// Hostname returns the computer's name.
func Hostname() string {
	h, _ := os.Hostname()
	return h
}

// CanElevate reports whether the agent can offer to relaunch as administrator.
func CanElevate() bool { return runtime.GOOS == "windows" && !IsElevated() }

// Permissions describes macOS privacy permissions the agent needs.
type Permissions struct {
	ScreenRecording bool
	Accessibility   bool
}

// OK reports whether everything needed is granted.
func (p Permissions) OK() bool { return p.ScreenRecording && p.Accessibility }

// Relaunch starts a new copy of the agent with args. On macOS the app bundle
// is reopened through Launch Services so the new process picks up privacy
// permissions granted since this one started.
func Relaunch(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
			bundle := exe[:i+len(".app")]
			return exec.Command("open", append([]string{"-n", bundle, "--args"}, args...)...).Start()
		}
	}
	return exec.Command(exe, args...).Start()
}
