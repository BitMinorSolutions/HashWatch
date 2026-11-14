package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Roles. "admin" can use controls and settings; "view" can only look.
const (
	RoleAdmin = "admin"
	RoleView  = "view"
)

const ErrViewOnly = "View-only access: controls and settings are turned off for this sign-in."

const sessTTL = 30 * 24 * time.Hour

type session struct {
	ID       string    `json:"id"` // public id (not the cookie)
	Role     string    `json:"role"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
	Exp      time.Time `json:"-"`
	IP       string    `json:"ip"`
	UA       string    `json:"ua"`
	Remote   bool      `json:"remote"` // signed in through the remote link
	Via      string    `json:"via"`    // password | password+2fa | recovery | view-password | view-link
}

func randToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// sessionOf returns the caller's live session (and refreshes last-seen).
func (s *Server) sessionOf(r *http.Request) *session {
	c, err := r.Cookie("hw_session")
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	se := s.sessions[c.Value]
	if se == nil {
		return nil
	}
	now := s.Now()
	if now.After(se.Exp) {
		delete(s.sessions, c.Value)
		return nil
	}
	se.LastSeen, se.IP = now, clientIP(r)
	if ua := r.UserAgent(); ua != "" {
		se.UA = trimUA(ua)
	}
	return se
}

func trimUA(ua string) string {
	if len(ua) > 200 {
		return ua[:200]
	}
	return ua
}

// role is the caller's role, or "" when not signed in. With no password set,
// visitors on this computer / home network are admin; nobody gets in through
// the remote link without a password.
func (s *Server) role(r *http.Request) string {
	if s.Cfg.PasswordHash == "" {
		if viaTunnel(r) {
			return ""
		}
		return RoleAdmin
	}
	if se := s.sessionOf(r); se != nil {
		return se.Role
	}
	return ""
}

func (s *Server) authed(r *http.Request) bool { return s.role(r) != "" }

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, 401, map[string]string{"error": "auth"})
			return
		}
		h(w, r)
	}
}

// adminOnly refuses view-only sessions (read endpoints that reveal links, IPs or settings).
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.role(r) != RoleAdmin {
			writeJSON(w, 403, map[string]string{"error": ErrViewOnly})
			return
		}
		h(w, r)
	}
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request, role string) {
	s.issueVia(w, r, role, "password")
}

func (s *Server) issueVia(w http.ResponseWriter, r *http.Request, role, via string) {
	tok := randToken(32)
	now := s.Now()
	s.mu.Lock()
	s.sessions[tok] = &session{ID: randToken(6), Role: role, Created: now, LastSeen: now, Exp: now.Add(sessTTL), IP: clientIP(r),
		UA: trimUA(r.UserAgent()), Remote: viaTunnel(r), Via: via}
	s.mu.Unlock()
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" || viaTunnel(r)
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

// ---------------- lockout ----------------

// Per address: 5 wrong tries are free, then each further wrong try locks that
// address out for 1, 2, 4… minutes (max 60). Across the remote link as a whole,
// 30 wrong tries within 15 minutes pause all remote sign-ins for 15 minutes
// (so rotating addresses doesn't help an attacker). Home-network sign-in keeps working.
const (
	freeTries     = 5
	maxLock       = time.Hour
	globalWindow  = 15 * time.Minute
	globalMax     = 30
	globalLockFor = 15 * time.Minute
)

type failRec struct {
	n     int
	until time.Time
	last  time.Time
}

type limiter struct {
	fails       map[string]*failRec
	remote      []time.Time
	remoteUntil time.Time
}

// blocked returns how long ip must still wait.
func (s *Server) blocked(ip string, remote bool) time.Duration {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if remote && now.Before(s.lim.remoteUntil) {
		return s.lim.remoteUntil.Sub(now)
	}
	if f := s.lim.fails[ip]; f != nil && now.Before(f.until) {
		return f.until.Sub(now)
	}
	return 0
}

func (s *Server) fail(ip string, remote bool) {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.lim.fails[ip]
	if f == nil || now.Sub(f.last) > 24*time.Hour {
		f = &failRec{}
		s.lim.fails[ip] = f
	}
	f.n++
	f.last = now
	if f.n >= freeTries {
		lock := time.Minute << uint(min(f.n-freeTries, 6))
		f.until = now.Add(min(lock, maxLock))
	}
	if remote {
		var keep []time.Time
		for _, t := range s.lim.remote {
			if now.Sub(t) < globalWindow {
				keep = append(keep, t)
			}
		}
		s.lim.remote = append(keep, now)
		if len(s.lim.remote) >= globalMax {
			s.lim.remoteUntil = now.Add(globalLockFor)
			s.lim.remote = nil
		}
	}
}

func (s *Server) succeed(ip string) {
	s.mu.Lock()
	delete(s.lim.fails, ip)
	s.mu.Unlock()
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	msg := "Too many wrong tries. Try again in " + strconv.Itoa(secs) + " seconds."
	if secs > 90 {
		msg = fmt.Sprintf("Too many wrong tries. Try again in %d minutes.", (secs+59)/60)
	}
	writeJSON(w, 429, map[string]any{"error": msg, "retry_after": secs})
}

// ---------------- sign in ----------------

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip, remote := clientIP(r), viaTunnel(r)
	if wait := s.blocked(ip, remote); wait > 0 {
		tooMany(w, wait)
		return
	}
	var body struct{ Password string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	role := ""
	switch {
	case s.Cfg.PasswordHash != "" && s.Cfg.CheckPassword(body.Password):
		role = RoleAdmin
	case s.Cfg.Security.ViewHash != "" && hashPw(body.Password, s.Cfg.Security.ViewSalt) != "" &&
		subtle.ConstantTimeCompare([]byte(hashPw(body.Password, s.Cfg.Security.ViewSalt)), []byte(s.Cfg.Security.ViewHash)) == 1:
		role = RoleView
	}
	if role == "" {
		s.fail(ip, remote)
		if wait := s.blocked(ip, remote); wait > 0 {
			tooMany(w, wait)
			return
		}
		writeJSON(w, 401, map[string]string{"error": "Incorrect password."})
		return
	}
	via := "password"
	if role == RoleView {
		via = "view-password"
	}
	s.succeed(ip)
	s.issueVia(w, r, role, via)
	writeJSON(w, 200, map[string]any{"ok": true, "role": role})
}

// viewLink signs a visitor in as view-only from a shared link (/v/<token>).
func (s *Server) viewLink(w http.ResponseWriter, r *http.Request) {
	ip, remote := clientIP(r), viaTunnel(r)
	if wait := s.blocked(ip, remote); wait > 0 {
		tooMany(w, wait)
		return
	}
	want, got := s.Cfg.Security.ViewLink, r.PathValue("token")
	if want == "" || s.Cfg.PasswordHash == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		s.fail(ip, remote)
		http.Error(w, "This view-only link is no longer valid.", http.StatusNotFound)
		return
	}
	s.succeed(ip)
	s.issueVia(w, r, RoleView, "view-link")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------------- sessions ----------------

// sessionsRevoke signs out every device except the one asking ("Sign out everywhere").
func (s *Server) sessionsRevoke(w http.ResponseWriter, r *http.Request) {
	me := s.sessionOf(r)
	n := s.revokeAll(func(se *session) bool { return me != nil && se.ID == me.ID })
	writeJSON(w, 200, map[string]any{"ok": true, "signed_out": n})
}

func (s *Server) revokeAll(keep func(*session) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for tok, se := range s.sessions {
		if keep == nil || !keep(se) {
			delete(s.sessions, tok)
			n++
		}
	}
	return n
}

// ---------------- security settings ----------------

func (s *Server) securityGet(w http.ResponseWriter, r *http.Request) {
	sec := s.Cfg.Security
	out := map[string]any{"password_set": s.Cfg.PasswordHash != "",
		"view_password_set": sec.ViewHash != "", "view_link_on": sec.ViewLink != "",
		"remote_on": s.Cfg.Remote.Enabled, "lockout": map[string]any{"free_tries": freeTries, "max_lock_min": int(maxLock.Minutes()),
			"remote_max": globalMax, "remote_window_min": int(globalWindow.Minutes())},
		"signing_note": "The Mac app isn't signed with an Apple Developer ID yet (that needs a paid Apple account), so macOS asks you to confirm the first launch."}
	if sec.ViewLink != "" {
		out["view_link_path"] = "/v/" + sec.ViewLink
		if s.Tunnel != nil {
			if u := s.Tunnel.Status().URL; u != "" {
				out["view_link_url"] = strings.TrimRight(u, "/") + "/v/" + sec.ViewLink
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) viewPassword(w http.ResponseWriter, r *http.Request) {
	var b struct{ Password string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b)
	if s.Cfg.PasswordHash == "" {
		writeJSON(w, 409, map[string]string{"error": "Set your main password first."})
		return
	}
	if b.Password != "" && len(b.Password) < 6 {
		writeJSON(w, 400, map[string]string{"error": "Use at least 6 characters."})
		return
	}
	if b.Password != "" && s.Cfg.CheckPassword(b.Password) {
		writeJSON(w, 400, map[string]string{"error": "Use a different password from your main one."})
		return
	}
	s.Cfg.mu.Lock()
	if b.Password == "" {
		s.Cfg.Security.ViewHash, s.Cfg.Security.ViewSalt = "", ""
	} else {
		salt := make([]byte, 16)
		rand.Read(salt)
		s.Cfg.Security.ViewSalt = hex.EncodeToString(salt)
		s.Cfg.Security.ViewHash = hashPw(b.Password, s.Cfg.Security.ViewSalt)
	}
	s.Cfg.mu.Unlock()
	s.Cfg.Save()
	if b.Password == "" { // removing it signs out password-based viewers
		s.revokeAll(func(se *session) bool { return !(se.Role == RoleView && se.Via == "view-password") })
	}
	s.securityGet(w, r)
}

func (s *Server) viewLinkSet(w http.ResponseWriter, r *http.Request) {
	var b struct{ Action string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b)
	if s.Cfg.PasswordHash == "" {
		writeJSON(w, 409, map[string]string{"error": "Set your main password first."})
		return
	}
	s.Cfg.mu.Lock()
	switch b.Action {
	case "create", "rotate":
		s.Cfg.Security.ViewLink = randToken(18)
	case "off":
		s.Cfg.Security.ViewLink = ""
	default:
		s.Cfg.mu.Unlock()
		writeJSON(w, 400, map[string]string{"error": "action must be create, rotate or off"})
		return
	}
	s.Cfg.mu.Unlock()
	s.Cfg.Save()
	// the old link's viewers lose access
	s.revokeAll(func(se *session) bool { return se.Via != "view-link" })
	s.securityGet(w, r)
}

// remoteRotate gets a new free link (and signs out everyone who came in through the old one).
func (s *Server) remoteRotate(w http.ResponseWriter, r *http.Request) {
	if s.Tunnel == nil || s.Locked || !s.Cfg.Remote.Enabled {
		writeJSON(w, 409, map[string]string{"error": "Turn on remote access first."})
		return
	}
	if s.Cfg.Remote.Token != "" {
		writeJSON(w, 409, map[string]string{"error": "Your permanent link can't be rotated here. Use “Sign out everywhere” instead, or change the hostname in Cloudflare."})
		return
	}
	remoteMu.Lock()
	s.Tunnel.Stop()
	s.Tunnel.Start()
	remoteMu.Unlock()
	n := s.revokeAll(func(se *session) bool { return !se.Remote })
	writeJSON(w, 200, map[string]any{"ok": true, "signed_out": n, "status": s.Tunnel.Status()})
}
