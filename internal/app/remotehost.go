package app

import (
	"io/fs"
	"net"
	"net/http"
	"strings"
)

// RemoteAppSuffix is appended to AppName when the dashboard is opened through
// a Tailscale address, so the iPhone Home Screen shortcut is distinct from LAN.
const RemoteAppSuffix = " Remote"

// RemoteAppName is the product title used for Tailscale Home Screen shortcuts.
func RemoteAppName() string { return AppName + RemoteAppSuffix }

// requestHost is the hostname (no port) the visitor used to reach HashWatch.
func requestHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	h := r.Host
	if h == "" {
		h = r.URL.Host
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	h = strings.TrimRight(h, ".")
	return h
}

// ViaTailscaleHost reports whether this request was made to a Tailscale
// address: a 100.64.0.0/10 IP or a *.ts.net name (port optional).
// LAN names such as hashwatch.local or 192.168.x are not Tailscale.
func ViaTailscaleHost(r *http.Request) bool {
	host := requestHost(r)
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "ts.net" || strings.HasSuffix(lower, ".ts.net") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && tailscaleCGNAT.Contains(ip)
}

func requestAppName(r *http.Request) string {
	if ViaTailscaleHost(r) {
		return RemoteAppName()
	}
	return AppName
}

func iconBase(r *http.Request) string {
	if ViaTailscaleHost(r) {
		return "/assets/icons/remote"
	}
	return "/assets/icons"
}

func (s *Server) serveHostIcon(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := "icons/" + name
		if ViaTailscaleHost(r) {
			path = "icons/remote/" + name
		}
		b, err := fs.ReadFile(s.Static, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Vary", "Host")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	}
}
