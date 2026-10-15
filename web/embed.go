// Package web embeds the built single-page app (web/dist) into the server.
// Run `npm ci && npm run build` in this directory before building the server.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built app rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
