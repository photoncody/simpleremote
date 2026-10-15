// Package ui is the agent's window. It shows who is connecting, asks the user
// for consent, and stays visible for the whole session with an End button.
package ui

import (
	_ "embed"
	"fmt"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/photoncody/simpleremote/agent/internal/core"
	"github.com/photoncody/simpleremote/agent/internal/platform"
	"github.com/photoncody/simpleremote/internal/proto"
	"github.com/photoncody/simpleremote/internal/stamp"
)

//go:embed icon.svg
var iconSVG []byte

// Options configure the window.
type Options struct {
	ServerURL     string // empty: ask the user
	Code          string // may be prefilled from the file name
	ResumeToken   string
	OrgName       string
	Version       string
	AutoAllow     bool
	FromAutostart bool
	Session       func(core.Options) *core.Session // for tests; defaults to core.New
}

// App is the agent window.
type App struct {
	opts Options
	a    fyne.App
	w    fyne.Window
	sess *core.Session

	state          string // "setup", "connecting", "consent", "active", "ended"
	techName       string
	chatBox        *fyne.Container
	chatScroll     *container.Scroll
	activityBox    *fyne.Container
	activityScroll *container.Scroll
	persistBanner  *widget.Label
	persistOn      bool
}

// Run shows the window and blocks until it closes.
func Run(opts Options) {
	if opts.Session == nil {
		opts.Session = core.New
	}
	a := app.NewWithID("com.github.photoncody.simpleremote")
	icon := fyne.NewStaticResource("icon.svg", iconSVG)
	a.SetIcon(icon)
	u := &App{opts: opts, a: a}
	u.w = a.NewWindow("SimpleRemote")
	u.w.SetIcon(icon)
	u.w.Resize(fyne.NewSize(440, 540))
	u.w.SetCloseIntercept(u.onClose)
	u.w.CenterOnScreen()

	if opts.ServerURL != "" && (opts.Code != "" || opts.ResumeToken != "") {
		u.start(opts.ServerURL, opts.Code)
	} else {
		u.showSetup()
	}
	u.w.ShowAndRun()
}

func (u *App) setContent(title string, body fyne.CanvasObject) {
	heading := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	heading.SizeName = theme.SizeNameHeadingText
	org := u.opts.OrgName
	if h := u.hello(); h.OrgName != "" {
		org = h.OrgName
	}
	top := container.NewVBox(heading)
	if org != "" {
		top.Add(widget.NewLabelWithStyle("Remote support · "+org, fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
	}
	top.Add(widget.NewSeparator())
	u.w.SetContent(container.NewPadded(container.NewBorder(top, nil, nil, nil, body)))
}

func (u *App) hello() proto.Hello {
	if u.sess == nil {
		return proto.Hello{}
	}
	return u.sess.Hello()
}

func wrapLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// ---- setup (no stamp) ----

func (u *App) showSetup() {
	u.state = "setup"
	server := widget.NewEntry()
	server.SetPlaceHolder("https://support.example.com")
	server.SetText(u.opts.ServerURL)
	code := widget.NewEntry()
	code.SetPlaceHolder("123456")
	code.SetText(u.opts.Code)
	errLabel := widget.NewLabel("")
	errLabel.Importance = widget.DangerImportance
	connect := widget.NewButton("Connect", nil)
	connect.Importance = widget.HighImportance
	connect.OnTapped = func() {
		s := strings.TrimRight(strings.TrimSpace(server.Text), "/")
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		pu, err := url.Parse(s)
		if err != nil || pu.Host == "" || (pu.Scheme != "https" && pu.Scheme != "http") {
			errLabel.SetText("Enter the support website address.")
			return
		}
		c := stamp.NormalizeCode(code.Text)
		if !stamp.IsCode(c) {
			errLabel.SetText("The code has 6 digits.")
			return
		}
		u.start(s, c)
	}
	form := widget.NewForm(widget.NewFormItem("Support website", server), widget.NewFormItem("Code", code))
	u.setContent("Connect to your technician", container.NewVBox(
		wrapLabel("Enter the website and code your support technician gave you."),
		form, errLabel, connect,
	))
}

// ---- session lifecycle ----

func (u *App) start(server, code string) {
	u.sess = u.opts.Session(core.Options{
		ServerURL: server, Code: code, ResumeToken: u.opts.ResumeToken,
		Version: u.opts.Version, UI: u, AutoAllow: u.opts.AutoAllow, FromAutostart: u.opts.FromAutostart,
	})
	go u.sess.Run()
}

// Connecting implements core.UI.
func (u *App) Connecting(server string) {
	fyne.Do(func() {
		u.state = "connecting"
		bar := widget.NewProgressBarInfinite()
		u.setContent("Connecting…", container.NewVBox(wrapLabel("Contacting "+server), bar))
	})
}

// Reconnecting implements core.UI.
func (u *App) Reconnecting() {
	u.Activity("Connection lost, reconnecting…")
}

// AskConsent implements core.UI.
func (u *App) AskConsent(h proto.Hello) {
	fyne.Do(func() {
		u.state = "consent"
		u.techName = h.TechName
		u.w.RequestFocus()
		who := h.TechName
		if h.TechEmail != "" && h.TechEmail != h.TechName {
			who += " (" + h.TechEmail + ")"
		}
		msg := widget.NewRichTextFromMarkdown(fmt.Sprintf(
			"**%s** from **%s** wants to see and control this computer.", escapeMD(who), escapeMD(h.OrgName)))
		msg.Wrapping = fyne.TextWrapWord
		warn := wrapLabel("Only allow this if you asked " + h.OrgName + " for help, or you're talking with them right now. " +
			"Never allow someone who contacted you unexpectedly.")
		warn.Importance = widget.WarningImportance
		can := wrapLabel("If you allow it, they can see your screen, use your mouse and keyboard, and exchange files with you. " +
			"You can end the session at any time.")
		deny := widget.NewButton("Deny", func() { u.sess.Respond(false) })
		allow := widget.NewButton("Allow", func() { u.sess.Respond(true) })
		allow.Importance = widget.HighImportance
		buttons := container.NewGridWithColumns(2, deny, allow)
		u.setContent("Allow remote support?", container.NewVBox(msg, warn, can, layout.NewSpacer(), buttons))
	})
}

// NeedPermissions implements core.UI.
func (u *App) NeedPermissions(p platform.Permissions) {
	fyne.Do(func() {
		var missing []string
		if !p.ScreenRecording {
			missing = append(missing, "Screen Recording")
		}
		if !p.Accessibility {
			missing = append(missing, "Accessibility")
		}
		restart := widget.NewButton("Restart SimpleRemote", func() {
			// The new process resumes this session, so the user doesn't need
			// a new code.
			if err := u.sess.Restart(); err != nil {
				dialog.ShowError(err, u.w)
				return
			}
			u.a.Quit()
		})
		restart.Importance = widget.HighImportance
		d := dialog.NewCustom("Permission needed", "Later", container.NewVBox(
			wrapLabel("macOS needs your permission before the technician can help: "+strings.Join(missing, " and ")+"."),
			wrapLabel("In System Settings → Privacy & Security, turn on SimpleRemote for each one, "+
				"then click Restart SimpleRemote. Your session stays connected."),
			widget.NewButton("Open System Settings", platform.OpenPermissionSettings),
			restart,
		), u.w)
		d.Resize(fyne.NewSize(400, 300))
		d.Show()
	})
}

// Active implements core.UI.
func (u *App) Active(h proto.Hello) {
	fyne.Do(func() {
		if u.state == "active" {
			return
		}
		u.state = "active"
		u.techName = h.TechName
		u.w.SetTitle("SimpleRemote — session active")

		status := widget.NewRichTextFromMarkdown(fmt.Sprintf("● **%s** can see and control this computer.", escapeMD(h.TechName)))
		status.Wrapping = fyne.TextWrapWord
		end := widget.NewButtonWithIcon("End session", theme.CancelIcon(), u.confirmEnd)
		end.Importance = widget.DangerImportance
		actions := container.NewHBox(end)
		if platform.CanElevate() {
			adm := widget.NewButtonWithIcon("Run as administrator", theme.WarningIcon(), func() { u.askElevate(false) })
			actions.Add(adm)
		}

		u.chatBox = container.NewVBox()
		u.chatScroll = container.NewVScroll(u.chatBox)
		u.chatScroll.SetMinSize(fyne.NewSize(0, 160))
		entry := widget.NewEntry()
		entry.SetPlaceHolder("Message your technician")
		send := func() {
			u.sess.SendChat(entry.Text)
			entry.SetText("")
		}
		entry.OnSubmitted = func(string) { send() }
		chatRow := container.NewBorder(nil, nil, nil, widget.NewButton("Send", send), entry)

		u.activityBox = container.NewVBox()
		u.activityScroll = container.NewVScroll(u.activityBox)
		u.activityScroll.SetMinSize(fyne.NewSize(0, 90))

		u.persistBanner = wrapLabel("")
		u.persistBanner.Importance = widget.WarningImportance
		u.persistBanner.Hide()

		tabs := container.NewAppTabs(
			container.NewTabItem("Chat", container.NewBorder(nil, chatRow, nil, nil, u.chatScroll)),
			container.NewTabItem("Activity", u.activityScroll),
		)
		u.setContent("Remote session active", container.NewBorder(
			container.NewVBox(status, actions, u.persistBanner, widget.NewSeparator()), nil, nil, nil, tabs))
		u.applyPersistBanner()
		u.Activity("Session started.")
		u.setTray(true)
		u.a.SendNotification(fyne.NewNotification("Remote session active", h.TechName+" can see and control this computer."))
	})
}

// PersistState implements core.UI.
func (u *App) PersistState(on bool) {
	fyne.Do(func() {
		u.persistOn = on
		u.applyPersistBanner()
	})
}

// applyPersistBanner shows or hides the "reconnects after restart" note.
// Must be called on the UI goroutine.
func (u *App) applyPersistBanner() {
	if u.persistBanner == nil {
		return
	}
	if u.persistOn {
		u.persistBanner.SetText("↻ This computer will reconnect to your technician after a restart, until the session ends. " +
			"Click End session to stop.")
		u.persistBanner.Show()
	} else {
		u.persistBanner.Hide()
	}
}

// Chat implements core.UI.
func (u *App) Chat(from, text string) {
	fyne.Do(func() {
		if u.chatBox == nil {
			return
		}
		l := wrapLabel(from + ": " + text)
		if from == "You" {
			l.Importance = widget.LowImportance
		} else {
			l.TextStyle = fyne.TextStyle{Bold: true}
			u.w.RequestFocus()
		}
		u.chatBox.Add(l)
		u.chatScroll.ScrollToBottom()
	})
}

// Activity implements core.UI.
func (u *App) Activity(text string) {
	fyne.Do(func() {
		if u.activityBox == nil {
			return
		}
		u.activityBox.Add(wrapLabel(time.Now().Format("15:04") + "  " + text))
		u.activityScroll.ScrollToBottom()
	})
}

// ElevationRequested implements core.UI.
func (u *App) ElevationRequested(techName string) {
	fyne.Do(func() { u.askElevate(true) })
}

func (u *App) askElevate(requested bool) {
	msg := "Restart SimpleRemote as administrator? This lets the technician work with programs and settings that need " +
		"administrator rights. Windows will ask you to confirm."
	if requested {
		msg = u.techName + " asks to restart SimpleRemote as administrator so they can work with programs and settings " +
			"that need administrator rights. Windows will ask you to confirm."
	}
	dialog.ShowCustomConfirm("Run as administrator?", "Restart as admin", "Not now", wrapLabel(msg), func(ok bool) {
		if !ok {
			if requested {
				u.sess.DeclineElevation()
			}
			return
		}
		if err := u.sess.Elevate(); err != nil {
			if err != platform.ErrCancelled {
				dialog.ShowError(err, u.w)
			}
			return
		}
		// The elevated copy takes over the session.
		u.a.Quit()
	}, u.w)
}

// Ended implements core.UI.
func (u *App) Ended(reason string) {
	fyne.Do(func() {
		if u.state == "ended" {
			return
		}
		prev := u.state
		u.state = "ended"
		u.w.SetTitle("SimpleRemote")
		u.setTray(false)
		closeBtn := widget.NewButton("Close", u.a.Quit)
		closeBtn.Importance = widget.HighImportance
		body := container.NewVBox(wrapLabel(reason))
		if prev == "setup" || prev == "connecting" {
			body.Add(wrapLabel("Check the code with your technician. Codes work once and expire after a few minutes."))
		} else {
			body.Add(wrapLabel("The technician can no longer see or control this computer."))
		}
		body.Add(layout.NewSpacer())
		body.Add(closeBtn)
		u.setContent("Session ended", body)
		if prev == "active" {
			// Close on our own after a while; nothing is left running.
			time.AfterFunc(30*time.Second, func() { fyne.Do(u.a.Quit) })
		}
	})
}

func (u *App) confirmEnd() {
	dialog.ShowConfirm("End session?", "The technician will be disconnected.", func(ok bool) {
		if ok {
			u.sess.End()
		}
	}, u.w)
}

func (u *App) onClose() {
	switch u.state {
	case "consent":
		u.sess.Respond(false)
	case "active", "connecting":
		if u.state == "active" {
			u.confirmEnd()
			return
		}
		u.sess.End()
	}
	u.a.Quit()
}

func (u *App) setTray(active bool) {
	desk, ok := u.a.(desktop.App)
	if !ok {
		return
	}
	if !active {
		desk.SetSystemTrayMenu(fyne.NewMenu("SimpleRemote"))
		return
	}
	desk.SetSystemTrayMenu(fyne.NewMenu("SimpleRemote",
		fyne.NewMenuItem("Remote session active", func() { u.w.Show(); u.w.RequestFocus() }),
		fyne.NewMenuItem("End session", func() { u.w.Show(); u.confirmEnd() }),
	))
}

func escapeMD(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "#", `\#`)
	return r.Replace(s)
}
