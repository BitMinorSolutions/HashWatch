package main

import (
	"strings"

	"github.com/hashwatch/hashwatch/internal/app"
)

// shellOpts configures the native app shell (macOS window + menu bar, Windows window + tray).
type shellOpts struct {
	URL     string // dashboard URL (http://127.0.0.1:PORT/…, this process's own server)
	Origin  string // http://127.0.0.1:PORT: the window never shows any other origin
	Hidden  bool   // start in the menu bar / tray without a window (auto-start)
	Srv     *app.Server
	Quit    func() // stops everything (cloudflared too) and exits
	Name    string
	Tagline string
	Version string
	Dir     string // settings folder (window state on Windows)
}

// sameOrigin reports whether u belongs to this copy's own server (exact scheme, host and port).
func sameOrigin(u, origin string) bool {
	if origin == "" {
		return false
	}
	return u == origin || strings.HasPrefix(u, origin+"/") || strings.HasPrefix(u, origin+"?") || strings.HasPrefix(u, origin+"#")
}
