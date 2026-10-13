//go:build !webui

// Package web holds the web app (plan §3.7). Without the tag webui (unit tests, `go build ./...`
// before the app is built) the binary embeds none; `chaosgw api --web-dir` can still serve one.
package web

import "io/fs"

// Dist returns nil: this binary was built without the web app.
func Dist() fs.FS { return nil }
