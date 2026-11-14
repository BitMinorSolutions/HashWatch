package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
)

type Server struct {
	Cfg     *Config
	Poll    *Poller
	Static  fs.FS
	Locked  bool // demo/preview mode: miners and password cannot be changed
	Version string
	Port    int
	LANOK   bool // listening on all interfaces, so phone URLs work

	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string][]time.Time
}

const sessTTL = 30 * 24 * time.Hour

func (s *Server) Handler() http.Handler {
	s.sessions, s.attempts = map[string]time.Time{}, map[string][]time.Time{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assets()))
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/live", s.auth(s.live))
	mux.HandleFunc("POST /api/miners", s.auth(s.mutating(s.addMiner)))
	mux.HandleFunc("DELETE /api/miners/{id}", s.auth(s.mutating(s.delMiner)))
	mux.HandleFunc("POST /api/discover", s.auth(s.mutating(s.discover)))
	mux.HandleFunc("POST /api/password", s.auth(s.mutating(s.password)))
	return secHeaders(mux)
}

func secHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self'")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) assets() http.Handler {
	fsrv := http.FileServer(http.FS(s.Static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "vendor/") || strings.HasPrefix(r.URL.Path, "icons/") {
			w.Header().Set("Cache-Control", "public, max-age=604800")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fsrv.ServeHTTP(w, r)
	})
}

func (s *Server) tpl(w http.ResponseWriter, name, ctype string) {
	b, err := fs.ReadFile(s.Static, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(strings.ReplaceAll(string(b), "{{APP}}", AppName)))
}
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	s.tpl(w, "index.html", "text/html; charset=utf-8")
}
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	s.tpl(w, "manifest.webmanifest", "application/manifest+json")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) authed(r *http.Request) bool {
	if s.Cfg.PasswordHash == "" {
		return true
	}
	c, err := r.Cookie("hw_session")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	return ok && time.Now().Before(exp)
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]string{"error": "auth"})
			return
		}
		h(w, r)
	}
}

// mutating guards settings changes: same-origin JSON only, and never in demo mode.
func (s *Server) mutating(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Locked {
			writeJSON(w, 403, map[string]string{"error": "Settings are locked in this preview."})
			return
		}
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
			writeJSON(w, 403, map[string]string{"error": "cross-site request"})
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, 415, map[string]string{"error": "json required"})
			return
		}
		h(w, r)
	}
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-Ip"); ip != "" {
		return ip
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	ok := s.authed(r)
	out := map[string]any{"app": AppName, "version": s.Version, "auth_required": s.Cfg.PasswordHash != "", "authed": ok,
		"locked": s.Locked, "poll_seconds": int(s.Poll.Every / time.Second)}
	if ok {
		var ms []map[string]any
		for _, m := range s.Cfg.Miners {
			ms = append(ms, map[string]any{"id": m.ID, "name": m.Name, "host": m.Host, "port": m.Port, "kind": m.Kind})
		}
		out["miners"] = ms
		out["setup_needed"] = len(ms) == 0
		var lan []string
		if !s.Locked && s.LANOK {
			for _, a := range LANAddrs() {
				lan = append(lan, "http://"+a+":"+strconv.Itoa(s.Port))
			}
		}
		out["lan_urls"] = lan
	}
	writeJSON(w, 200, out)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	now := time.Now()
	s.mu.Lock()
	var recent []time.Time
	for _, t := range s.attempts[ip] {
		if now.Sub(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	s.attempts[ip] = recent
	s.mu.Unlock()
	if len(recent) >= 10 {
		writeJSON(w, 429, map[string]string{"error": "Too many attempts. Try again in a few minutes."})
		return
	}
	var body struct{ Password string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	if s.Cfg.PasswordHash == "" || !s.Cfg.CheckPassword(body.Password) {
		s.mu.Lock()
		s.attempts[ip] = append(s.attempts[ip], now)
		s.mu.Unlock()
		writeJSON(w, 401, map[string]string{"error": "Incorrect password."})
		return
	}
	s.issue(w, r)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.sessions[tok] = time.Now().Add(sessTTL)
	s.mu.Unlock()
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{Name: "hw_session", Value: tok, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessTTL / time.Second)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("hw_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "hw_session", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("miner")
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	v, ok := s.Poll.Live(id, since)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "unknown miner"})
		return
	}
	writeJSON(w, 200, map[string]any{"server_time": time.Now().Unix(), "data": v.Latest, "last_ok": v.LastOK,
		"last_err": v.LastErr, "history": v.History})
}

func (s *Server) addMiner(w http.ResponseWriter, r *http.Request) {
	var m Miner
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&m); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	m.Host = strings.TrimSpace(m.Host)
	if h, p, err := net.SplitHostPort(m.Host); err == nil {
		m.Host = h
		m.Port, _ = strconv.Atoi(p)
	}
	if m.Port == 0 {
		m.Port = 4028
	}
	if m.Host == "" || strings.ContainsAny(m.Host, "/ ") {
		writeJSON(w, 400, map[string]string{"error": "Enter the miner's IP address, like 192.168.1.50."})
		return
	}
	desc, ok := cgminer.Probe(net.JoinHostPort(m.Host, strconv.Itoa(m.Port)), 4*time.Second)
	if !ok {
		writeJSON(w, 422, map[string]string{"error": "No miner answered on " + m.Host + ":" + strconv.Itoa(m.Port) + ". Check the IP and that the miner's API is enabled."})
		return
	}
	m.ID, m.Kind = NewID(), "cgminer"
	if strings.TrimSpace(m.Name) == "" {
		m.Name = desc
	}
	s.Cfg.mu.Lock()
	s.Cfg.Miners = append(s.Cfg.Miners, m)
	s.Cfg.mu.Unlock()
	if err := s.Cfg.Save(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	s.Poll.Start(m)
	writeJSON(w, 200, map[string]any{"ok": true, "id": m.ID, "desc": desc})
}

func (s *Server) delMiner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.Cfg.mu.Lock()
	var keep []Miner
	for _, m := range s.Cfg.Miners {
		if m.ID != id {
			keep = append(keep, m)
		}
	}
	s.Cfg.Miners = keep
	s.Cfg.mu.Unlock()
	s.Cfg.Save()
	s.Poll.Stop(id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"found": Discover()})
}

func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	var b struct{ Current, New string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	if s.Cfg.PasswordHash != "" && !s.Cfg.CheckPassword(b.Current) {
		writeJSON(w, 401, map[string]string{"error": "Current password is incorrect."})
		return
	}
	if b.New != "" && len(b.New) < 6 {
		writeJSON(w, 400, map[string]string{"error": "Use at least 6 characters."})
		return
	}
	s.Cfg.SetPassword(b.New)
	s.Cfg.Save()
	s.mu.Lock()
	s.sessions = map[string]time.Time{}
	s.mu.Unlock()
	if b.New != "" {
		s.issue(w, r)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
