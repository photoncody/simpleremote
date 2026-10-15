package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestReusableSetupRequiresCodeAndShowsServer(t *testing.T) {
	for _, server := range []string{"", "https://support.example.com"} {
		t.Run(server, func(t *testing.T) {
			a := test.NewApp()
			defer a.Quit()
			u := &App{a: a, opts: Options{ServerURL: server}}
			u.w = a.NewWindow("SimpleRemote")
			defer u.w.Close()
			u.showSetup()
			var form *widget.Form
			var connect *widget.Button
			var labels []*widget.Label
			var visit func(fyne.CanvasObject)
			visit = func(o fyne.CanvasObject) {
				switch v := o.(type) {
				case *fyne.Container:
					for _, child := range v.Objects {
						visit(child)
					}
				case *widget.Form:
					form = v
				case *widget.Button:
					if v.Text == "Connect" {
						connect = v
					}
				case *widget.Label:
					labels = append(labels, v)
				}
			}
			visit(u.w.Content())
			if form == nil || connect == nil {
				t.Fatal("missing setup form")
			}
			website := form.Items[0].Widget.(*widget.Entry)
			code := form.Items[1].Widget.(*widget.Entry)
			if website.Text != server || code.Text != "" || u.sess != nil {
				t.Fatal("setup did not preserve server and wait for code")
			}
			website.SetText("https://support.example.com")
			test.Tap(connect)
			found := false
			for _, l := range labels {
				if l.Text == "The code has 6 digits." {
					found = true
				}
			}
			if !found || u.sess != nil {
				t.Fatal("blank code did not block connection")
			}
			// Render the form with the headless Fyne driver as part of the UI check.
			u.w.Resize(fyne.NewSize(440, 540))
			if u.w.Canvas().Capture().Bounds().Empty() {
				t.Fatal("empty setup render")
			}
		})
	}
}
