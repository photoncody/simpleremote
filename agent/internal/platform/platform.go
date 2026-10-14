// Package platform wraps OS-specific details: elevation, DPI awareness, and
// macOS privacy permissions.
package platform

import (
	"os"
	"os/user"
	"runtime"
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
