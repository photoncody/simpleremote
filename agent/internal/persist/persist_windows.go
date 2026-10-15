//go:build windows

package persist

import "golang.org/x/sys/windows/registry"

// runKey is the current user's Run key: entries here start once at the next
// login. It needs no administrator rights and affects only this user.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

const runValue = "SimpleRemote"

func enableAutostart(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// The token is read from the 0600 resume file, not the command line.
	return k.SetStringValue(runValue, `"`+exe+`" --from-autostart`)
}

func disableAutostart() {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.DeleteValue(runValue)
}
