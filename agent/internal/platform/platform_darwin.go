//go:build darwin

package platform

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

static bool screenAllowed(void) { return CGPreflightScreenCaptureAccess(); }
static void requestScreen(void) { CGRequestScreenCaptureAccess(); }
static bool axAllowed(void) { return AXIsProcessTrusted(); }
static void requestAX(void) {
	const void *keys[] = { kAXTrustedCheckOptionPrompt };
	const void *vals[] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1,
		&kCFCopyStringDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
}
*/
import "C"

import (
	"errors"
	"os"
	"os/exec"
)

// Init does nothing on macOS.
func Init() {}

// IsElevated reports whether the process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// ErrCancelled is unused on macOS.
var ErrCancelled = errors.New("cancelled")

// RelaunchElevated is not supported on macOS.
func RelaunchElevated([]string) error { return errors.New("not supported on macOS") }

// CheckPermissions reports whether Screen Recording and Accessibility are
// granted to this app.
func CheckPermissions() Permissions {
	return Permissions{ScreenRecording: bool(C.screenAllowed()), Accessibility: bool(C.axAllowed())}
}

// RequestPermissions asks macOS to show its permission prompts. The user
// grants them in System Settings; the app must restart to pick up Screen
// Recording.
func RequestPermissions() {
	p := CheckPermissions()
	if !p.ScreenRecording {
		C.requestScreen()
	}
	if !p.Accessibility {
		C.requestAX()
	}
}

// OpenPermissionSettings opens the relevant System Settings pane.
func OpenPermissionSettings() {
	p := CheckPermissions()
	pane := "Privacy_Accessibility"
	if !p.ScreenRecording {
		pane = "Privacy_ScreenCapture"
	}
	_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?"+pane).Start()
}
