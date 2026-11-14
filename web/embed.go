// Package web embeds the dashboard frontend.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS is the static frontend rooted at web/static.
var FS, _ = fs.Sub(files, "static")
