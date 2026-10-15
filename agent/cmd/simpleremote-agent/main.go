// Command simpleremote-agent is the program the end user runs to let a
// support technician see and control their computer.
//
// It is normally downloaded from the SimpleRemote server, which stamps the
// server address and session code into the file. It installs nothing, asks
// the user before sharing anything, and exits when the session ends.
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/photoncody/simpleremote/agent/internal/persist"
	"github.com/photoncody/simpleremote/agent/internal/platform"
	"github.com/photoncody/simpleremote/agent/internal/ui"
	"github.com/photoncody/simpleremote/internal/stamp"
)

var version = "dev"

// defaultServer can be set at build time (-X main.defaultServer=https://...)
// for builds that can't be stamped, such as notarized macOS apps. The session
// code then comes from the download's file name.
var defaultServer = ""

func main() {
	server := flag.String("server", "", "server URL (normally read from the downloaded file)")
	code := flag.String("code", "", "session code (normally read from the downloaded file)")
	resume := flag.String("resume", "", "internal: resume token when relaunching as administrator")
	org := flag.String("org", "", "internal: organization name")
	owner := flag.String("owner", "", "internal: the account of the user who allowed the session")
	fromAutostart := flag.Bool("from-autostart", false, "internal: resume a session after the computer restarted")
	logPath := flag.String("log", "", "write a debug log to this file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	var logOut io.Writer = os.Stderr
	level := slog.LevelInfo
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			defer f.Close()
			logOut = f
			level = slog.LevelDebug
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: level})))

	platform.Init()

	// Relaunched by the per-user autostart after a restart: rejoin the saved
	// session, or clean up and exit if it can no longer be resumed.
	if *fromAutostart {
		info, ok := persist.Load()
		switch {
		case !ok || info.Expired():
			slog.Info("autostart: no resumable session", "found", ok)
			persist.Disarm()
			return
		case info.Owner != "" && info.Owner != platform.Username():
			// Only the user who allowed the session resumes it.
			slog.Info("autostart: session belongs to another user", "owner", info.Owner)
			persist.Disarm()
			return
		}
		ui.Run(ui.Options{
			ServerURL: info.ServerURL, ResumeToken: info.ResumeToken, OrgName: info.OrgName,
			Owner: info.Owner, Version: version, FromAutostart: true,
		})
		return
	}

	opts := ui.Options{
		ServerURL: *server, Code: stamp.NormalizeCode(*code), ResumeToken: *resume, OrgName: *org,
		Owner: *owner, Version: version, AutoAllow: autoAllow(),
	}
	if opts.ServerURL == "" && opts.ResumeToken == "" {
		if st, path, err := readStamp(); err == nil {
			opts.ServerURL, opts.Code, opts.OrgName = st.ServerURL, st.Code, st.OrgName
		} else {
			slog.Info("no stamp found", "err", err)
			opts.ServerURL = defaultServer
			if opts.Code == "" {
				opts.Code = stamp.CodeFromFilename(path)
			}
		}
	}
	ui.Run(opts)
}

// readStamp reads connection details from this executable. It also returns
// the path that names the download (the .app bundle on macOS), for the
// file-name fallback.
func readStamp() (stamp.Stamp, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return stamp.Stamp{}, "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	name := exe
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		name = exe[:i+len(".app")]
	}
	st, err := stamp.ReadFile(exe)
	return st, name, err
}
