//go:build linux

package screen

import (
	"io"
	"log"

	"github.com/jezek/xgb"
)

// The X11 library logs a warning on every connection when ~/.Xauthority is
// missing, which is normal on many desktops. Keep the agent's output clean.
func init() { xgb.Logger = log.New(io.Discard, "", 0) }
