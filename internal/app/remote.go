package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/autostart"
	"github.com/hashwatch/hashwatch/internal/qr"
)

var remoteMu sync.Mutex // serialises enable/disable

func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"app": AppName, "id": s.InstanceID})
}

func (s *Server) remoteBrief() map[string]any {
	out := map[string]any{"available": s.Tunnel != nil && !s.Locked, "enabled": s.Cfg.Remote.Enabled}
	if s.Tunnel != nil {
		st := s.Tunnel.Status()
		out["state"], out["url"] = st.State, st.URL
	}
	return out
}

func (s *Server) remoteGet(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"available": s.Tunnel != nil && !s.Locked, "enabled": s.Cfg.Remote.Enabled,
		"local_viewer": IsLocal(r), "password_set": s.Cfg.PasswordHash != "", "hostname": s.Cfg.Remote.Hostname,
		"named": s.Cfg.Remote.Token != "", "last_url": s.Cfg.Remote.LastURL}
	if s.Locked {
		out["note"] = "Remote access is turned off in this preview. In your own copy of Hashwatch you can switch it on here."
	}
	if s.Tunnel != nil {
		out["status"] = s.Tunnel.Status()
	}
	if s.Auto.Exe != "" {
		st := autostart.Get(s.Auto)
		if autostart.Transient(s.Auto.Exe) {
			st.Note = "Move Hashwatch out of your Downloads or temporary folder first, then turn this on."
		}
		out["autostart"] = st
	}
	writeJSON(w, 200, out)
}

func (s *Server) remoteQR(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	if s.Tunnel == nil || u == "" || u != s.Tunnel.Status().URL { // only ever encodes our own link
		http.NotFound(w, r)
		return
	}
	c, err := qr.Encode([]byte(u))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(c.SVG("#0b0c0e", "#ffffff")))
}

func (s *Server) remoteSet(w http.ResponseWriter, r *http.Request) {
	if s.Tunnel == nil || s.Locked {
		writeJSON(w, 403, map[string]string{"error": "Remote access is turned off in this preview."})
		return
	}
	var b struct {
		Enabled         bool
		Password        string
		Token, Hostname *string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	remoteMu.Lock()
	defer remoteMu.Unlock()
	if s.Cfg.PasswordHash != "" && !s.authed(r) {
		writeJSON(w, 401, map[string]string{"error": "auth"})
		return
	}
	if b.Token != nil || b.Hostname != nil {
		tok, host := strings.TrimSpace(deref(b.Token)), strings.TrimSpace(deref(b.Hostname))
		host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"), "/")
		if (tok == "") != (host == "") || strings.ContainsAny(host, " /") || len(tok) > 4096 {
			writeJSON(w, 400, map[string]string{"error": "For a permanent link, enter both the tunnel token and its hostname (or clear both)."})
			return
		}
		s.Cfg.mu.Lock()
		s.Cfg.Remote.Token, s.Cfg.Remote.Hostname = tok, host
		s.Cfg.mu.Unlock()
		s.Tunnel.Token, s.Tunnel.Hostname = tok, host
		if s.Tunnel.Running() {
			s.Tunnel.Stop()
			s.Tunnel.Start()
		}
	}
	if b.Enabled {
		if s.Cfg.PasswordHash == "" {
			if len(b.Password) < 6 {
				writeJSON(w, 412, map[string]string{"error": "Create a password (at least 6 characters) first. Anyone with the link will need it."})
				return
			}
			s.Cfg.SetPassword(b.Password)
			s.mu.Lock()
			s.sessions = map[string]time.Time{}
			s.mu.Unlock()
			s.issue(w, r)
		}
		s.Cfg.Remote.Enabled = true
		s.Tunnel.Start()
	} else {
		s.Cfg.Remote.Enabled = false
		s.Tunnel.Stop()
	}
	if err := s.Cfg.Save(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "status": s.Tunnel.Status()})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TunnelURL is called by the tunnel when a link comes up; it remembers the
// link and raises a "link changed" alert when it differs from the last one.
func (s *Server) TunnelURL(u string) {
	s.Cfg.mu.Lock()
	old := s.Cfg.Remote.LastURL
	s.Cfg.Remote.LastURL = u
	s.Cfg.mu.Unlock()
	s.Cfg.Save()
	if old != "" && old != u && s.Alerts != nil {
		s.Alerts.Fire(Alert{Kind: "link", Level: "info", Title: "New remote link", URL: u,
			Body: "Your remote link changed. Open " + AppName + " at home and re-scan the QR code on your phone."})
	}
}

func (s *Server) autostartSet(w http.ResponseWriter, r *http.Request) {
	var b struct{ Enabled bool }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b)
	if s.Auto.Exe == "" {
		writeJSON(w, 400, map[string]string{"error": "Auto-start isn't available here."})
		return
	}
	var st autostart.Status
	var err error
	if b.Enabled {
		if autostart.Transient(s.Auto.Exe) {
			writeJSON(w, 409, map[string]string{"error": "Move Hashwatch out of your Downloads or temporary folder first (for example into Applications or your home folder), run it from there, then turn this on."})
			return
		}
		st, err = autostart.Enable(s.Auto)
	} else {
		st, err = autostart.Disable(s.Auto)
	}
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error(), "autostart": st})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "autostart": st})
}

func (s *Server) alertsGet(w http.ResponseWriter, r *http.Request) {
	items, latest := s.Alerts.Since(0, 0)
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 { // newest first
		items[i], items[j] = items[j], items[i]
	}
	var kinds []map[string]any
	for _, k := range AlertKinds {
		kinds = append(kinds, map[string]any{"kind": k.Kind, "label": k.Label, "desc": k.Desc, "on": s.Cfg.AlertOn(k.Kind)})
	}
	writeJSON(w, 200, map[string]any{"items": items, "latest": latest, "kinds": kinds,
		"desktop": !s.Cfg.NotifyOff && !s.Locked, "desktop_available": !s.Locked})
}

func (s *Server) alertsSet(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Kinds   map[string]bool
		Desktop *bool
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	s.Cfg.mu.Lock()
	if s.Cfg.AlertsOff == nil {
		s.Cfg.AlertsOff = map[string]bool{}
	}
	for _, k := range AlertKinds {
		if on, ok := b.Kinds[k.Kind]; ok {
			if on {
				delete(s.Cfg.AlertsOff, k.Kind)
			} else {
				s.Cfg.AlertsOff[k.Kind] = true
			}
		}
	}
	if b.Desktop != nil {
		s.Cfg.NotifyOff = !*b.Desktop
	}
	s.Cfg.mu.Unlock()
	s.Cfg.Save()
	s.alertsGet(w, r)
}

func (s *Server) alertsClear(w http.ResponseWriter, r *http.Request) {
	s.Alerts.Clear()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) alertsTest(w http.ResponseWriter, r *http.Request) {
	a, _ := s.Alerts.Fire(Alert{Kind: "test", Level: "info", Title: "Test notification", Body: "Alerts from " + AppName + " will look like this."})
	writeJSON(w, 200, map[string]any{"ok": true, "alert": a})
}
