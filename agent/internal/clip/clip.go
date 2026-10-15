// Package clip reads and writes the system clipboard (text only).
package clip

import (
	"context"
	"sync"

	"golang.design/x/clipboard"
)

var (
	once    sync.Once
	initErr error
)

// Init prepares the clipboard. It is safe to call repeatedly.
func Init() error {
	once.Do(func() { initErr = clipboard.Init() })
	return initErr
}

// Write puts text on the clipboard.
func Write(text string) {
	_, _ = clipboard.Write(context.Background(), clipboard.FmtText, []byte(text))
}

// Watch sends the clipboard text each time it changes until ctx is done.
// Text that the copying app marked as sensitive (for example a password
// manager) is never reported.
func Watch(ctx context.Context) <-chan string {
	out := make(chan string)
	in := clipboard.Watch(ctx, clipboard.FmtText)
	go func() {
		defer close(out)
		for d := range in {
			if d.Sensitive {
				continue
			}
			select {
			case out <- string(d.Bytes):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
