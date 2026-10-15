// Package persist lets a consented session survive a restart of the user's
// computer ("stay connected after restart").
//
// It is a temporary, per-user arrangement, never a system install:
//   - a resume file (0600) in the user's config dir holds the server URL and
//     resume token so the agent can rejoin the same session, and
//   - a per-user autostart entry (no service, no administrator rights)
//     relaunches the agent once after the next login.
//
// Both are removed by Disarm, which the agent calls when the session ends for
// any reason. The resume file also carries a hard expiry, so an agent that is
// relaunched but can no longer rejoin removes the autostart and exits.
package persist

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Info is what the relaunched agent needs to rejoin its session.
type Info struct {
	ServerURL   string    `json:"server_url"`
	ResumeToken string    `json:"resume_token"`
	OrgName     string    `json:"org_name"`
	Expires     time.Time `json:"expires"`
}

// Expired reports whether the resume window has passed.
func (i Info) Expired() bool { return time.Now().After(i.Expires) }

func dir() (string, error) {
	c, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "SimpleRemote"), nil
}

func filePath() (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "session.json"), nil
}

// Arm writes the resume file and installs the per-user autostart entry.
func Arm(info Info) error {
	if info.ResumeToken == "" || info.ServerURL == "" {
		return errors.New("persist: incomplete session info")
	}
	d, err := dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	path, _ := filePath()
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, lerr := filepath.EvalSymlinks(exe); lerr == nil {
		exe = p
	}
	if err := enableAutostart(exe); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// Disarm removes the autostart entry and the resume file. It is safe to call
// when nothing is armed.
func Disarm() {
	disableAutostart()
	if path, err := filePath(); err == nil {
		os.Remove(path)
	}
}

// Load reads the resume file written by Arm.
func Load() (Info, bool) {
	path, err := filePath()
	if err != nil {
		return Info{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}
	var info Info
	if json.Unmarshal(b, &info) != nil || info.ResumeToken == "" {
		return Info{}, false
	}
	return info, true
}

// Armed reports whether a resume file is present.
func Armed() bool {
	_, ok := Load()
	return ok
}
