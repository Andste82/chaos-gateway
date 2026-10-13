//go:build webui

// Package web holds the web app (plan §3.7). Built with the tag webui, the binary embeds the
// app's build output (web/dist, `make build-web`); `make build` does both.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the embedded build of the web app.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the directory is embedded above: cannot happen
	}
	return sub
}
