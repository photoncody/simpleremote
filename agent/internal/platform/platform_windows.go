//go:build windows

package platform

import (
	"errors"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	user32                        = windows.NewLazySystemDLL("user32.dll")
	procSetProcessDpiAwarenessCtx = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware        = user32.NewProc("SetProcessDPIAware")
)

// Init makes the process DPI-aware so screen capture and input use physical
// pixels on scaled displays.
func Init() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	if procSetProcessDpiAwarenessCtx.Find() == nil {
		if r, _, _ := procSetProcessDpiAwarenessCtx.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	if procSetProcessDPIAware.Find() == nil {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

// IsElevated reports whether the process runs as administrator.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// ErrCancelled means the user declined the UAC prompt.
var ErrCancelled = errors.New("the administrator prompt was cancelled")

// RelaunchElevated starts this executable again with administrator rights,
// showing the standard UAC prompt. args are passed to the new process.
func RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	err = windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return ErrCancelled
	}
	return err
}

// CheckPermissions always succeeds on Windows.
func CheckPermissions() Permissions { return Permissions{true, true} }

// RequestPermissions does nothing on Windows.
func RequestPermissions() {}

// OpenPermissionSettings does nothing on Windows.
func OpenPermissionSettings() {}
