// Package autostart registers Hashwatch to start in the background when the
// user logs in: a LaunchAgent on macOS, a Run-key entry (via a hidden launcher)
// on Windows, and a systemd user service on Linux (XDG autostart as fallback).
package autostart

import (
	"os"
	"path/filepath"
	"strings"
)

// Label is the service identifier.
const Label = "com.hashwatch.agent"

// Spec describes what to run.
type Spec struct {
	Exe     string   // absolute path of the hashwatch binary
	Args    []string // e.g. -no-browser -config <dir>
	Dir     string   // config dir (for logs / launcher scripts)
	Home    string   // user's home (overridable in tests)
	AppName string
}

// Status is reported to the UI.
type Status struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Note      string `json:"note,omitempty"`
}

// Exe returns the running binary's real path.
func Exe() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func home(s Spec) string {
	if s.Home != "" {
		return s.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// Transient reports paths an app shouldn't be registered from (downloads
// that macOS translocates, or temp folders), so we can warn the user.
func Transient(exe string) bool {
	l := strings.ToLower(exe)
	return strings.Contains(l, "apptranslocation") || strings.Contains(l, string(filepath.Separator)+"tmp"+string(filepath.Separator)) || strings.Contains(l, `\temp\`)
}
