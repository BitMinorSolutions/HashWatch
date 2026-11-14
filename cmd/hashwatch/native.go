package main

import "github.com/hashwatch/hashwatch/internal/app"

// shellOpts configures the native app shell (macOS window + menu bar, Windows window + tray).
type shellOpts struct {
	URL     string // dashboard URL (127.0.0.1)
	Hidden  bool   // start in the menu bar / tray without a window (auto-start)
	Srv     *app.Server
	Quit    func() // stops everything (cloudflared too) and exits
	Name    string
	Tagline string
	Version string
	Dir     string // settings folder (window state on Windows)
}
