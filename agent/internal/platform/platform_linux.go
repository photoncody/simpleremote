//go:build linux

package platform

import (
	"errors"
	"os"
)

// Init does nothing on Linux.
func Init() {}

// IsElevated reports whether the process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// ErrCancelled is unused on Linux.
var ErrCancelled = errors.New("cancelled")

// RelaunchElevated is not supported on Linux.
func RelaunchElevated([]string) error { return errors.New("not supported on Linux") }

// CheckPermissions always succeeds on Linux (X11 has no per-app permissions).
func CheckPermissions() Permissions { return Permissions{true, true} }

// RequestPermissions does nothing on Linux.
func RequestPermissions() {}

// OpenPermissionSettings does nothing on Linux.
func OpenPermissionSettings() {}
