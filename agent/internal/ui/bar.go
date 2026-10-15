package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/photoncody/simpleremote/agent/internal/screen"
)

// While a session is live the agent shows a slim bar across the top of the
// screen instead of a window in the way of the user's work. It stays above
// other windows so the user can always see that someone is connected and end
// the session in one click. The full window is still there behind it for chat,
// activity and prompts.
const (
	barWidth  = 620
	barHeight = 44
	// barInset keeps the bar just below the top edge, clear of most menu bars.
	barInset = 2
)

// showSessionBar opens the top-of-screen session bar. It returns false when
// this system can't place a borderless window, so the caller keeps using the
// ordinary window.
func (u *App) showSessionBar(techName string) bool {
	drv, ok := u.a.Driver().(desktop.Driver)
	if !ok {
		return false
	}
	w := drv.CreateSplashWindow()
	dw, ok := w.(desktop.Window)
	if !ok {
		w.Close()
		return false
	}

	status := widget.NewLabel("")
	status.TextStyle = fyne.TextStyle{Bold: true}
	u.barStatus = status

	end := widget.NewButtonWithIcon("End session", theme.CancelIcon(), u.confirmEnd)
	end.Importance = widget.DangerImportance
	details := widget.NewButtonWithIcon("Chat", theme.MailComposeIcon(), func() {
		u.openMainWindow("chat")
	})
	u.barDetails = details

	w.SetContent(container.NewPadded(container.NewBorder(
		nil, nil,
		container.NewHBox(widget.NewIcon(theme.MediaRecordIcon()), status),
		container.NewHBox(details, end),
		// Nothing in the middle: the spacer keeps the buttons to the right.
		widget.NewLabel(""),
	)))
	w.Resize(fyne.NewSize(barWidth, barHeight))
	w.SetFixedSize(true)
	dw.RequestAlwaysOnTop()

	u.bar = w
	u.setBarStatus(techName)
	w.Show()
	// Position only once the window exists: a splash window centres itself
	// as it is created, which would discard an earlier request.
	u.placeBar(dw)
	return true
}

// placeBar moves the bar to the top centre of the primary display. It repeats
// the request shortly after, because the window may not have been created yet
// on the first try.
func (u *App) placeBar(dw desktop.Window) {
	place := func() {
		if u.bar != nil {
			dw.RequestPosition(barX(), barInset)
		}
	}
	place()
	time.AfterFunc(150*time.Millisecond, func() { fyne.Do(place) })
}

// barX centres the bar on the primary display. It falls back to a sensible
// offset when the display size isn't known.
func barX() int {
	for _, d := range screen.Displays() {
		if d.Primary {
			if x := d.X + (d.Width-barWidth)/2; x > d.X {
				return x
			}
			return d.X
		}
	}
	return 0
}

// setBarStatus updates the bar's one line of text.
func (u *App) setBarStatus(techName string) {
	if u.barStatus == nil {
		return
	}
	text := fmt.Sprintf("%s is connected to this computer", techName)
	if u.persistOn {
		text += " · reconnects after restart"
	}
	u.barStatus.SetText(text)
}

// flagBar draws attention to the bar when something needs the user, such as a
// new chat message or a file transfer.
func (u *App) flagBar(unread int) {
	if u.barDetails == nil {
		return
	}
	if unread > 0 {
		u.barDetails.SetText(fmt.Sprintf("Chat (%d)", unread))
		u.barDetails.Importance = widget.HighImportance
	} else {
		u.barDetails.SetText("Chat")
		u.barDetails.Importance = widget.MediumImportance
	}
	u.barDetails.Refresh()
}

// openMainWindow brings the full window forward, selecting a tab.
func (u *App) openMainWindow(tab string) {
	if u.tabs != nil {
		for i, item := range u.tabs.Items {
			if (tab == "chat" && item.Text == "Chat") || (tab == "activity" && item.Text == "Activity") {
				u.tabs.SelectIndex(i)
				break
			}
		}
	}
	u.unread = 0
	u.flagBar(0)
	u.w.Show()
	u.w.RequestFocus()
}

// closeSessionBar removes the bar, if one is showing.
func (u *App) closeSessionBar() {
	if u.bar != nil {
		u.bar.Close()
		u.bar = nil
		u.barStatus = nil
		u.barDetails = nil
	}
}
