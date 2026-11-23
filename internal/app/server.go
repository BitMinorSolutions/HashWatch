package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/autostart"
	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/install"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
	"github.com/hashwatch/hashwatch/internal/source"
	"github.com/hashwatch/hashwatch/internal/tunnel"
	"github.com/hashwatch/hashwatch/internal/update"
)

type Server struct {
	Cfg     *Config
	Poll    *Poller
	Static  fs.FS
	Locked  bool // demo/preview mode: miners and password cannot be changed
	Version string
	Port    int
	Host    string // bind address (127.0.0.1 or 0.0.0.0)
	LANOK   bool   // listening on all interfaces, so phone URLs work

	Tunnel     *tunnel.Manager // nil when remote access isn't possible (preview)
	Alerts     *Alerts
	AutoStart  autostart.Spec
	InstanceID string
	Quit       func()           // stops the server and cloudflared cleanly; nil in preview
	Install    install.Info     // "Move to Applications?" offer (macOS app only)
	Relaunch   func(app string) // stops this copy and starts the one at app
	Shell      string           // "browser", "macos-native", "windows-native"
	ShowWindow func()           // native shells: bring the window forward (second launch)
	Pi         *PiLink          // macOS app only: "Connect to my Pi" (nil = off)

	Net    *netdata.Fetcher // public network data (difficulty, blocks, price); nil = off
	Stats  *Stats           // lottery stats, badges, summaries
	Auto   *Automations     // schedule, light effects
	Fan    *FanSync         // re-applies a persisted Cooler fan speed
	Update *update.Runner   // OTA for the Pi service; never talks to a miner

	mu       sync.Mutex
	pwMu     sync.Mutex // serializes password changes so only one first-time set can win
	sessions map[string]*session
	lim      limiter
	Now      func() time.Time

	handler       http.Handler
	lnMu          sync.Mutex
	ln            net.Listener
	rebindPending bool
}

func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	if s.handler != nil {
		h := s.handler
		s.mu.Unlock()
		return h
	}
	s.sessions = map[string]*session{}
	s.lim = limiter{fails: map[string]*failRec{}}
	if s.Now == nil {
		s.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assets()))
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/live", s.auth(s.live))
	mux.HandleFunc("GET /api/ping", s.ping)
	mux.HandleFunc("POST /api/quit", s.auth(s.quitGuard(s.quit)))
	mux.HandleFunc("POST /api/install", s.auth(s.quitGuard(s.installSet)))
	mux.HandleFunc("GET /api/debug", s.auth(s.adminOnly(s.debug)))
	mux.HandleFunc("POST /api/show", s.quitGuard(s.show)) // second launch asks the running copy to show its window
	mux.HandleFunc("POST /api/pi/test", s.piOnly(s.mutating(s.piTest)))
	mux.HandleFunc("POST /api/pi/find", s.piOnly(s.mutating(s.piFind)))
	mux.HandleFunc("POST /api/pi/resolve", s.piOnly(s.mutating(s.piResolve)))
	mux.HandleFunc("POST /api/pi/connect", s.piOnly(s.auth(s.mutating(s.piConnect))))
	mux.HandleFunc("POST /api/pi/local", s.piOnly(s.auth(s.mutating(s.piLocal))))
	mux.HandleFunc("POST /api/miners", s.auth(s.admin(s.addMiner)))
	mux.HandleFunc("DELETE /api/miners/{id}", s.auth(s.admin(s.delMiner)))
	mux.HandleFunc("POST /api/discover", s.auth(s.admin(s.discover)))
	mux.HandleFunc("POST /api/password", s.auth(s.passwordGuard(s.password)))
	mux.HandleFunc("GET /api/remote", s.auth(s.adminOnly(s.remoteGet)))
	mux.HandleFunc("GET /api/remote/qr.svg", s.auth(s.adminOnly(s.remoteQR)))
	mux.HandleFunc("GET /api/tailscale", s.auth(s.adminOnly(s.tailscaleGet)))
	mux.HandleFunc("GET /api/tailscale/qr.svg", s.auth(s.adminOnly(s.tailscaleQR)))
	mux.HandleFunc("POST /api/tailscale/serve", s.auth(s.admin(s.tailscaleServe)))
	mux.HandleFunc("POST /api/remote/rotate", s.auth(s.admin(s.remoteRotate)))
	mux.HandleFunc("GET /v/{token}", s.viewLink)
	mux.HandleFunc("GET /api/security", s.auth(s.adminOnly(s.securityGet)))
	mux.HandleFunc("POST /api/security/view", s.auth(s.admin(s.viewPassword)))
	mux.HandleFunc("POST /api/sessions/revoke", s.auth(s.mutating(s.sessionsRevoke)))
	mux.HandleFunc("GET /api/lottery", s.auth(s.lotteryGet))
	mux.HandleFunc("GET /api/features", s.auth(s.featuresGet))
	mux.HandleFunc("POST /api/features", s.auth(s.mutating(s.featuresSet)))
	mux.HandleFunc("POST /api/privacy", s.auth(s.admin(s.privacySet)))
	mux.HandleFunc("POST /api/prefs", s.auth(s.admin(s.prefsSet)))
	mux.HandleFunc("GET /api/update", s.auth(s.adminOnly(s.updateStatus)))
	mux.HandleFunc("POST /api/update", s.auth(s.admin(s.updateSet)))
	mux.HandleFunc("POST /api/remote", s.admin(s.remoteSet)) // checks auth itself (may create the password)
	mux.HandleFunc("POST /api/autostart", s.auth(s.admin(s.autostartSet)))
	mux.HandleFunc("GET /api/alerts", s.auth(s.alertsGet))
	mux.HandleFunc("POST /api/alerts/settings", s.auth(s.mutating(s.alertsSet)))
	mux.HandleFunc("POST /api/alerts/clear", s.auth(s.mutating(s.alertsClear)))
	mux.HandleFunc("POST /api/alerts/test", s.auth(s.mutating(s.alertsTest)))
	mux.HandleFunc("GET /api/ntfy", s.auth(s.adminOnly(s.ntfyGet)))
	mux.HandleFunc("GET /api/ntfy/qr.svg", s.auth(s.adminOnly(s.ntfyQR)))
	mux.HandleFunc("POST /api/ntfy", s.auth(s.admin(s.ntfySet)))
	mux.HandleFunc("POST /api/ntfy/test", s.auth(s.admin(s.ntfyTest)))
	mux.HandleFunc("POST /api/control/workmode", s.auth(s.mutating(s.ctlWorkmode)))
	mux.HandleFunc("POST /api/control/led", s.auth(s.mutating(s.ctlLED)))
	mux.HandleFunc("POST /api/control/fan", s.auth(s.mutating(s.ctlFan)))
	mux.HandleFunc("POST /api/control/pool", s.auth(s.admin(s.ctlPool)))
	mux.HandleFunc("POST /api/control/reboot", s.auth(s.admin(s.ctlReboot)))
	s.handler = s.csrfWrap(secHeaders(mux))
	s.mu.Unlock()
	return s.handler
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
	w.Write([]byte(strings.NewReplacer("{{APP}}", AppName, "{{TAGLINE}}", Tagline).Replace(string(b))))
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

// mutating guards settings changes: same-origin JSON only, and never in demo mode.
func (s *Server) mutating(h http.HandlerFunc) http.HandlerFunc { return s.mutatingFirst(false, h) }

// mutatingFirst is mutating; with firstPassword set, a home-network or
// Tailscale device may also write while no password exists (only used for
// setting the first admin password).
func (s *Server) mutatingFirst(firstPassword bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Locked {
			writeJSON(w, 403, map[string]string{"error": "Settings are locked in this preview."})
			return
		}
		if viaTunnel(r) { // the share link is view-only, whatever the session
			writeJSON(w, 403, map[string]string{"error": ErrShareOnly})
			return
		}
		if s.role(r) == RoleView {
			writeJSON(w, 403, map[string]string{"error": ErrViewOnly})
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
		if s.Cfg.PasswordHash == "" && !IsLoopback(r) && !(firstPassword && CanSetFirstPassword(r)) {
			writeJSON(w, 403, map[string]string{"error": "Set an admin password in Settings first. Changes from other devices are blocked until you do."})
			return
		}
		h(w, r)
	}
}

func peerIP(r *http.Request) net.IP {
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return net.ParseIP(h)
}

// clientIP is the visitor's address; behind the tunnel that is Cloudflare's
// Cf-Connecting-Ip, trusted only when the request arrived from loopback.
func clientIP(r *http.Request) string {
	ip := peerIP(r)
	if ip != nil && ip.IsLoopback() {
		if c := r.Header.Get("Cf-Connecting-Ip"); c != "" {
			return c
		}
	}
	if ip == nil {
		return r.RemoteAddr
	}
	return ip.String()
}

// viaTunnel is true for requests that came through Cloudflare. Cloudflare
// always adds these headers, so a remote visitor can't strip them.
func viaTunnel(r *http.Request) bool {
	return r.Header.Get("Cf-Ray") != "" || r.Header.Get("Cf-Connecting-Ip") != "" || r.Header.Get("Cf-Visitor") != ""
}

// IsLoopback: the visitor is this computer itself, not a phone on Wi-Fi or the tunnel.
func IsLoopback(r *http.Request) bool {
	if viaTunnel(r) {
		return false
	}
	ip := peerIP(r)
	return ip != nil && ip.IsLoopback()
}

// IsLocal: the visitor is on this computer or the home network (or the
// owner's Tailscale), not the Cloudflare tunnel. See IsHomeIP.
func IsLocal(r *http.Request) bool {
	if viaTunnel(r) {
		return false
	}
	return IsHomeIP(peerIP(r))
}

// CanSetFirstPassword: this visitor may set the very first admin password.
// Same rule as every other "home network only" setting.
func CanSetFirstPassword(r *http.Request) bool { return IsLocal(r) }

// passwordGuard is admin for /api/password, except that while no password
// exists a home-network device (not only this computer) may set the first one.
func (s *Server) passwordGuard(h http.HandlerFunc) http.HandlerFunc {
	return s.mutatingFirst(true, func(w http.ResponseWriter, r *http.Request) {
		if !IsLocal(r) {
			writeJSON(w, 403, map[string]string{"error": "For safety, this can only be changed from your home network."})
			return
		}
		h(w, r)
	})
}

// admin guards settings that should only change from the home network
// (miners, password, remote access, auto-start).
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return s.mutating(func(w http.ResponseWriter, r *http.Request) {
		if !IsLocal(r) {
			writeJSON(w, 403, map[string]string{"error": "For safety, this can only be changed from your home network."})
			return
		}
		h(w, r)
	})
}

// displayMinerName is the name shown in the header and miner list. If the
// saved name is the doubled "Product Model" default (e.g. "Avalon Nano3s Nano3s"),
// the deduplicated model is shown instead. The saved config is not rewritten.
func displayMinerName(m Miner, snap *model.Snapshot) string {
	if snap != nil {
		clean := snap.Identity.DisplayModel()
		prod, mod := strings.TrimSpace(snap.Identity.Product), strings.TrimSpace(snap.Identity.Model)
		doubled := strings.TrimSpace(prod + " " + mod)
		if clean != "" && (m.Name == "" || m.Name == doubled) {
			return clean
		}
	}
	if m.Name != "" {
		return m.Name
	}
	return "Miner"
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	role := s.role(r)
	ok := role != ""
	out := map[string]any{"role": role, "view_only": role == RoleView, "optional": Optional, "app": AppName, "version": s.Version, "auth_required": s.Cfg.PasswordHash != "", "authed": ok,
		"locked": s.Locked, "poll_seconds": int(s.Poll.Every / time.Second), "can_quit": s.Quit != nil && !ServiceMode(), "tagline": Tagline, "shell": s.Shell, "service": ServiceMode(), "shared": viaTunnel(r)}
	if pi := s.piBrief(r); pi != nil {
		out["pi"] = pi
	}
	if tok := s.csrfToken(r); tok != "" {
		out["csrf"] = tok
	}
	wifi, gentle, advanced := s.Cfg.Prefs()
	out["allow_wifi"], out["gentle"], out["advanced_controls"] = wifi, gentle, advanced
	out["wifi_no_password"] = wifi && s.Cfg.PasswordHash == ""
	// Another device with no password set yet: setup asks for one first,
	// because writes from other devices are refused until it exists.
	if s.Cfg.PasswordHash == "" && !s.Locked && !IsLoopback(r) {
		out["password_first"] = true
		out["password_first_ok"] = CanSetFirstPassword(r)
	}
	if role == RoleAdmin || (ok && !s.Locked && role != RoleView) {
		out["update"] = s.updateBrief()
	}
	if s.Install.Offer && !s.Cfg.MoveDeclined && IsLocal(r) {
		out["install"] = s.Install
	}
	if ok {
		var ms []map[string]any
		for _, m := range s.Cfg.Miners {
			var snap *model.Snapshot
			if s.Poll != nil {
				snap = s.Poll.Latest(m.ID)
			}
			ms = append(ms, map[string]any{"id": m.ID, "name": displayMinerName(m, snap), "host": m.Host, "port": m.Port, "kind": m.Kind})
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
		out["local_viewer"] = IsLocal(r)
		if role == RoleAdmin {
			out["remote"] = s.remoteBrief()
			out["tailscale"] = s.tailscaleBrief()
		} else {
			out["lan_urls"] = nil
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("miner")
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	netSince, _ := strconv.ParseInt(r.URL.Query().Get("net"), 10, 64)
	alertSince, _ := strconv.ParseInt(r.URL.Query().Get("a"), 10, 64)
	hold := r.URL.Query().Get("hold") == "1"
	if hold {
		s.waitLive(r.Context(), id, since, netSince, alertSince)
	}
	v, ok := s.Poll.Live(id, since)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "unknown miner"})
		return
	}
	out := map[string]any{"server_time": time.Now().Unix(), "data": v.Latest, "last_ok": v.LastOK,
		"last_err": v.LastErr, "history": v.History, "poll_seconds": v.Poll, "next_poll_unix": v.NextAt, "can_control": s.canControl(id) && s.role(r) == RoleAdmin,
		"local_viewer": IsLocal(r), "view_only": s.role(r) == RoleView}
	if s.role(r) == RoleAdmin {
		out["remote"] = s.remoteBrief()
	}
	out["fan"] = s.fanView(id, v.Latest)
	mineSince := since
	if mineSince > 0 {
		mineSince -= 120 // last-share times can precede the poll timestamp
	}
	out["mining"] = s.Poll.MiningView(id, mineSince)
	if s.Alerts != nil {
		items, latest := s.Alerts.Since(alertSince, 20)
		out["alerts"], out["alerts_latest"] = items, latest
	}
	if _, ok := s.Cfg.minerByID(id); ok {
		lot := s.lotteryPayload(id)
		out["lottery"] = lot
		if net, ok := lot["net"].(map[string]any); ok {
			if rev, ok := net["rev"]; ok {
				out["net_rev"] = rev
			}
			out["net"] = net
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) waitLive(ctx context.Context, id string, since, netSince, alertSince int64) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	changed := func() bool {
		if s.Poll != nil && s.Poll.lastChangeUnix(id) > since {
			return true
		}
		if s.Net != nil {
			if s.Net.Get().Rev > netSince {
				return true
			}
		}
		if s.Alerts != nil {
			_, latest := s.Alerts.Since(alertSince, 1)
			if latest > alertSince {
				return true
			}
		}
		return false
	}
	if changed() {
		return
	}
	netCh := make(chan struct{}, 1)
	if s.Net != nil {
		go func() {
			s.Net.Wait(ctx, netSince)
			select {
			case netCh <- struct{}{}:
			default:
			}
		}()
	}
	minerCh := make(chan struct{}, 1)
	if s.Poll != nil {
		go func() {
			s.Poll.WaitChange(ctx, id, since)
			select {
			case minerCh <- struct{}{}:
			default:
			}
		}()
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-netCh:
			return
		case <-minerCh:
			return
		case <-tick.C:
			if changed() {
				return
			}
		}
	}
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
	s.startMiner(m)
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
	if s.Stats != nil {
		s.Stats.Remove(id)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"found": Discover()})
}

func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	var b struct{ Current, New string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	// Check-and-set under one lock: of two simultaneous first-time sets, the
	// second sees the new password and must know it.
	s.pwMu.Lock()
	defer s.pwMu.Unlock()
	if s.Cfg.PasswordHash == "" && b.New == "" {
		writeJSON(w, 400, map[string]string{"error": "Enter a password."})
		return
	}
	if s.Cfg.PasswordHash != "" && !IsLocal(r) {
		writeJSON(w, 403, map[string]string{"error": "For safety, this can only be changed from your home network."})
		return
	}
	if s.Cfg.PasswordHash != "" && !s.Cfg.CheckPassword(b.Current) {
		writeJSON(w, 401, map[string]string{"error": "Current password is incorrect."})
		return
	}
	if b.New == "" && s.Cfg.Remote.Enabled {
		writeJSON(w, 409, map[string]string{"error": "Turn off Share before removing the password."})
		return
	}
	if b.New != "" && len(b.New) < 6 {
		writeJSON(w, 400, map[string]string{"error": "Use at least 6 characters."})
		return
	}
	if b.New == "" && s.Cfg.Security.ViewHash != "" {
		writeJSON(w, 409, map[string]string{"error": "Remove the view-only password first."})
		return
	}
	s.Cfg.SetPassword(b.New)
	if b.New == "" {
		s.Cfg.mu.Lock()
		s.Cfg.Advanced = false
		s.Cfg.mu.Unlock()
	}
	s.Cfg.Save()
	s.mu.Lock()
	s.sessions = map[string]*session{}
	s.mu.Unlock()
	if b.New != "" {
		s.issue(w, r, RoleAdmin)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// canControl is true only for direct CGMiner miners outside preview mode.
func (s *Server) canControl(id string) bool {
	if s.Locked {
		return false
	}
	for _, m := range s.Cfg.Miners {
		if m.ID == id {
			return m.Kind == "cgminer"
		}
	}
	return false
}

func (s *Server) ctlReply(w http.ResponseWriter, res cgminer.Result, snap *model.Snapshot, err error) {
	out := map[string]any{"ok": err == nil && res.OK, "status": res.Status, "message": res.Message, "sent": res.Sent}
	if snap != nil {
		out["control"] = snap.Control
		out["data"] = snap
	}
	switch {
	case err == ErrNoControl:
		writeJSON(w, 403, map[string]string{"error": err.Error()})
	case err != nil && res.Sent == "":
		writeJSON(w, 409, map[string]string{"error": err.Error()})
	case err != nil:
		out["error"] = "Couldn't reach the miner: " + err.Error()
		writeJSON(w, 502, out)
	case !res.OK:
		out["error"] = "The miner refused the change: " + res.Message
		writeJSON(w, 422, out)
	default:
		writeJSON(w, 200, out)
	}
}

var levelNames = map[string]int{"low": 0, "medium": 1, "high": 2}

func (s *Server) ctlWorkmode(w http.ResponseWriter, r *http.Request) {
	var b struct{ Miner, Mode, Password string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	lvl, ok := levelNames[strings.ToLower(b.Mode)]
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "mode must be low, medium or high"})
		return
	}
	if !s.requireMinerPassword(w, b.Password) {
		return
	}
	if !s.canControl(b.Miner) {
		writeJSON(w, 403, map[string]string{"error": ErrNoControl.Error()})
		return
	}
	res, snap, err := s.Poll.Control(b.Miner, func(c source.Controller) (cgminer.Result, error) {
		res, err := c.SetWorkMode(lvl)
		if err == nil && res.OK && s.Fan != nil {
			s.Fan.ApplyIfCooler(c, b.Miner)
		}
		return res, err
	}, nil)
	s.ctlReply(w, res, snap, err)
}

func (s *Server) ctlLED(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Miner, Mode, Color, Password string
		Brightness                   int
		Temper                       *int
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	if !s.requireMinerPassword(w, b.Password) {
		return
	}
	eff := -1
	for i, m := range cgminer.LEDModes {
		if m == strings.ToLower(b.Mode) {
			eff = i
		}
	}
	temper := 50
	if b.Temper != nil {
		temper = *b.Temper
	}
	if _, err := cgminer.LEDParam(eff, b.Brightness, temper, b.Color); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if !s.canControl(b.Miner) {
		writeJSON(w, 403, map[string]string{"error": ErrNoControl.Error()})
		return
	}
	br, tp := b.Brightness, temper
	memo := &model.Control{LEDHex: strings.ToLower("#" + strings.TrimPrefix(b.Color, "#")), LEDBright: &br, LEDTemper: &tp}
	res, snap, err := s.Poll.Control(b.Miner, func(c source.Controller) (cgminer.Result, error) {
		return c.SetLED(eff, b.Brightness, temper, b.Color)
	}, memo)
	if err == nil && res.OK { // the user's own light: light effects restore to this
		s.Cfg.updateMiner(b.Miner, func(m *Miner) {
			m.Opts.Light.Base = &LightState{Mode: cgminer.LEDModes[eff], Color: memo.LEDHex, Bright: br}
		})
		s.Cfg.Save()
		if s.Auto != nil {
			s.Auto.LightChanged(b.Miner)
		}
	}
	s.ctlReply(w, res, snap, err)
}

func (s *Server) ctlFan(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Miner, Mode, Password string
		Percent               *int
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	mode := strings.ToLower(strings.TrimSpace(b.Mode))
	speed := cgminer.FanAuto
	pct := cgminer.FanDefault
	switch mode {
	case "auto":
		speed = cgminer.FanAuto
	case "cooler":
		if b.Percent != nil {
			pct = *b.Percent
		}
		speed = pct
		if _, err := cgminer.FanSpdParam(speed); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	default:
		writeJSON(w, 400, map[string]string{"error": "mode must be auto or cooler"})
		return
	}
	if !s.requireMinerPassword(w, b.Password) {
		return
	}
	if !s.canControl(b.Miner) {
		writeJSON(w, 403, map[string]string{"error": ErrNoControl.Error()})
		return
	}
	ok := s.Cfg.updateMiner(b.Miner, func(m *Miner) {
		m.Opts.Fan = FanOpts{Mode: mode, Percent: pct}
	})
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "unknown miner"})
		return
	}
	s.Cfg.Save()
	if mode == "auto" {
		s.Fan.ClearPending(b.Miner)
	} else {
		s.Fan.MarkPending(b.Miner)
	}
	res, snap, err := s.Poll.Control(b.Miner, func(c source.Controller) (cgminer.Result, error) {
		return c.SetFanSpd(speed)
	}, nil)
	if err == nil && res.OK && mode == "cooler" {
		s.Fan.ClearPending(b.Miner)
	}
	s.ctlReply(w, res, snap, err)
}

// quit answers first, then shuts down (tunnel, history, instance file) and exits.
func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	if s.Quit == nil {
		writeJSON(w, 403, map[string]string{"error": "Quit isn't available here."})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() { time.Sleep(300 * time.Millisecond); s.Quit() }()
}

// quitGuard is admin without the preview lock: a private preview build can still be quit,
// but only same-origin JSON from this computer's network (never through a tunnel).
func (s *Server) quitGuard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
			writeJSON(w, 403, map[string]string{"error": "cross-site request"})
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, 415, map[string]string{"error": "json required"})
			return
		}
		if !IsLocal(r) {
			writeJSON(w, 403, map[string]string{"error": "For safety, this can only be changed from your home network."})
			return
		}
		h(w, r)
	}
}

// installSet handles the "Move to Applications?" prompt: {"action":"move"|"dismiss"}.
func (s *Server) installSet(w http.ResponseWriter, r *http.Request) {
	var b struct{ Action string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b); err != nil || !s.Install.Offer {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if b.Action == "dismiss" {
		s.Cfg.mu.Lock()
		s.Cfg.MoveDeclined = true
		s.Cfg.mu.Unlock()
		s.Cfg.Save()
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	dst, err := install.Move(s.Install.From, install.AppsDir())
	if err != nil {
		msg := "Couldn't copy " + s.Install.Name + " to Applications: " + err.Error() + "."
		if errors.Is(err, install.ErrNoPermission) {
			msg = "Your Mac account can't write to the Applications folder. Drag " + s.Install.Name + " into Applications in Finder instead (it may ask for an administrator password)."
		}
		writeJSON(w, 409, map[string]any{"error": msg, "permission": errors.Is(err, install.ErrNoPermission)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": dst, "id": s.InstanceID})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if s.Relaunch != nil {
		go func() { time.Sleep(300 * time.Millisecond); s.Relaunch(dst) }()
	}
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	if s.ShowWindow == nil || !peerIP(r).IsLoopback() {
		writeJSON(w, 404, map[string]bool{"ok": false})
		return
	}
	s.ShowWindow()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
