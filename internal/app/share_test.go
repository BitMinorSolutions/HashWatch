package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/tunnel"
)

// shareServer: password set, Share on with a (fake) quick tunnel online.
func shareServer(t *testing.T) (*Server, *tclient, string) {
	t.Helper()
	t.Setenv("HASHWATCH_CLOUDFLARED", fakeCloudflared(t))
	ts, s := newTestServer(t, nil, false, "")
	s.Tunnel = &tunnel.Manager{Dir: t.TempDir(), Origin: ts.URL, OnURL: s.TunnelURL}
	t.Cleanup(s.Tunnel.Stop)
	home := &tclient{t: t, ts: ts}
	if r, out := home.do("POST", "/api/remote", `{"enabled":true,"password":"secret1"}`); r.StatusCode != 200 {
		t.Fatalf("share on: %d %v", r.StatusCode, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Tunnel.Status().State != "online" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return s, home, "/v/" + s.Cfg.Security.ViewLink
}

func TestShareShowsOnlyTheViewLink(t *testing.T) {
	s, home, path := shareServer(t)
	if len(path) < 10 {
		t.Fatalf("no link token: %q", path)
	}
	_, out := home.do("GET", "/api/remote", "")
	b, _ := json.Marshal(out)
	st, _ := out["status"].(map[string]any)
	want := "https://test-link-abc.trycloudflare.com" + path
	if st["url"] != want {
		t.Fatalf("status url %v, want %s", st["url"], want)
	}
	if strings.Contains(strings.ReplaceAll(string(b), want, ""), "trycloudflare.com") {
		t.Fatalf("admin address leaked: %s", b)
	}
	_, state := home.do("GET", "/api/state", "")
	if rem, _ := state["remote"].(map[string]any); rem["url"] != want {
		t.Fatalf("state.remote %v", state["remote"])
	}
	_ = s
}

func TestShareTunnelIsViewOnly(t *testing.T) {
	s, home, path := shareServer(t)
	far := &tclient{t: t, ts: home.ts, ip: "203.0.113.50"}

	// no session through the tunnel: the "shared by link only" page, no login
	_, st := far.do("GET", "/api/state", "")
	if st["shared"] != true || st["authed"] != false {
		t.Fatalf("tunnel state: %v", st)
	}
	// the admin password doesn't sign in through the tunnel
	if r, out := far.do("POST", "/api/login", `{"password":"secret1"}`); r.StatusCode != 403 || far.ck != nil {
		t.Fatalf("tunnel admin login: %d %v", r.StatusCode, out)
	}
	// the share link signs in as view-only
	if r, _ := far.do("GET", path, ""); r.StatusCode != 303 {
		t.Fatalf("share link: %d", r.StatusCode)
	}
	if _, st := far.do("GET", "/api/state", ""); st["role"] != RoleView || st["view_only"] != true {
		t.Fatalf("link role: %v", st)
	}
	if r, _ := far.do("POST", "/api/prefs", `{"gentle":true}`); r.StatusCode != 403 {
		t.Fatalf("viewer write: %d", r.StatusCode)
	}

	// an admin cookie from home, copied onto the tunnel host, is refused
	stolen := &tclient{t: t, ts: home.ts, ip: "203.0.113.51", ck: home.ck, csrf: home.csrf, tok: home.tok}
	writes := [][3]string{
		{"POST", "/api/prefs", `{"gentle":true}`},
		{"POST", "/api/miners", `{"host":"192.168.1.9"}`},
		{"POST", "/api/remote", `{"enabled":false}`},
		{"POST", "/api/remote/rotate", `{}`},
		{"POST", "/api/password", `{"current":"secret1","new":"hijack1"}`},
		{"POST", "/api/security/view", `{"password":"peekaboo"}`},
		{"POST", "/api/sessions/revoke", `{}`},
		{"POST", "/api/features", `{"miner":"m","fun":{"summary":"off"}}`},
		{"POST", "/api/alerts/clear", `{}`},
		{"POST", "/api/control/workmode", `{"miner":"m","mode":"low"}`},
	}
	reads := []string{"/api/remote", "/api/security", "/api/debug", "/api/update", "/api/tailscale", "/api/ntfy", "/api/remote/qr.svg?u=x"}
	for _, w := range writes {
		if r, out := stolen.do(w[0], w[1], w[2]); r.StatusCode != 403 && r.StatusCode != 401 {
			t.Errorf("stolen cookie %s %s: %d %v", w[0], w[1], r.StatusCode, out)
		}
	}
	for _, p := range reads {
		if r, _ := stolen.do("GET", p, ""); r.StatusCode != 403 && r.StatusCode != 401 {
			t.Errorf("stolen cookie GET %s: %d", p, r.StatusCode)
		}
	}
	if _, st := stolen.do("GET", "/api/state", ""); st["role"] == RoleAdmin || st["authed"] == true {
		t.Fatalf("stolen cookie state: %v", st)
	}
	if !s.Cfg.CheckPassword("secret1") || !s.Cfg.Remote.Enabled {
		t.Fatal("something changed through the tunnel")
	}

	// the same cookie at home is still a full admin
	if r, _ := home.do("POST", "/api/prefs", `{"gentle":true}`); r.StatusCode != 200 {
		t.Fatalf("home admin write: %d", r.StatusCode)
	}
	for _, p := range reads[:2] {
		if r, _ := home.do("GET", p, ""); r.StatusCode != 200 {
			t.Fatalf("home admin GET %s: %d", p, r.StatusCode)
		}
	}
}

func TestShareNewLinkAndOff(t *testing.T) {
	s, home, path := shareServer(t)
	far := &tclient{t: t, ts: home.ts, ip: "203.0.113.60"}
	far.do("GET", path, "")
	if _, st := far.do("GET", "/api/state", ""); st["role"] != RoleView {
		t.Fatal("link viewer")
	}

	// New link: new token, the old link and its viewers are out
	r, out := home.do("POST", "/api/remote/rotate", `{}`)
	if r.StatusCode != 200 || out["signed_out"].(float64) != 1 {
		t.Fatalf("rotate: %d %v", r.StatusCode, out)
	}
	path2 := "/v/" + s.Cfg.Security.ViewLink
	if path2 == path {
		t.Fatal("token not rotated")
	}
	if _, st := far.do("GET", "/api/state", ""); st["authed"] == true {
		t.Fatal("old viewer still signed in after New link")
	}
	g := &tclient{t: t, ts: home.ts, ip: "203.0.113.61"}
	if r, _ := g.do("GET", path, ""); r.StatusCode != 404 {
		t.Fatalf("old link after New link: %d", r.StatusCode)
	}
	if r, _ := g.do("GET", path2, ""); r.StatusCode != 303 {
		t.Fatalf("new link: %d", r.StatusCode)
	}

	// Share off: tunnel stops, link 404s, its viewers are signed out
	if r, _ := home.do("POST", "/api/remote", `{"enabled":false}`); r.StatusCode != 200 || s.Tunnel.Running() {
		t.Fatal("share off")
	}
	if _, st := g.do("GET", "/api/state", ""); st["authed"] == true {
		t.Fatal("viewer still signed in after Share off")
	}
	g2 := &tclient{t: t, ts: home.ts, ip: "203.0.113.62"}
	if r, _ := g2.do("GET", path2, ""); r.StatusCode != 404 {
		t.Fatalf("link after Share off: %d", r.StatusCode)
	}
	if r, _ := home.do("GET", path2, ""); r.StatusCode != 404 {
		t.Fatalf("link at home after Share off: %d", r.StatusCode)
	}
	// home admin unaffected
	if r, _ := home.do("POST", "/api/prefs", `{"gentle":false}`); r.StatusCode != 200 {
		t.Fatalf("home admin after share off: %d", r.StatusCode)
	}
	if r, _ := home.do("POST", "/api/remote/rotate", `{}`); r.StatusCode != 409 {
		t.Fatalf("New link while off: %d", r.StatusCode)
	}
}

// A config from before Share (remote access on, no link token) gets a link.
func TestShareMigration(t *testing.T) {
	dir := t.TempDir()
	writeFile(dir+"/config.json", `{"password_hash":"x","password_salt":"00","remote":{"enabled":true}}`)
	cfg, _ := LoadConfig(dir)
	s := &Server{Cfg: cfg}
	s.EnsureShareLink()
	c2, _ := LoadConfig(dir)
	if !c2.Remote.Enabled || len(c2.Security.ViewLink) < 10 {
		t.Fatalf("migration: %+v", c2.Security)
	}
}

// Home network, Tailscale and this computer keep full admin sign-in.
func TestShareDoesNotTouchHomeAdmin(t *testing.T) {
	s, home, _ := shareServer(t)
	for _, addr := range []string{"192.168.1.50:1", "100.121.124.114:1", "127.0.0.1:1"} {
		c := &tclient{t: t, ts: home.ts, csrf: home.csrf, tok: home.tok}
		if r, out := c.as(s, addr, "POST", "/api/login", `{"password":"secret1"}`, nil); r.StatusCode != 200 || out["role"] != RoleAdmin {
			t.Fatalf("%s login: %d %v", addr, r.StatusCode, out)
		}
		if r, out := c.as(s, addr, "POST", "/api/prefs", `{"gentle":true}`, nil); r.StatusCode != 200 {
			t.Fatalf("%s write: %d %v", addr, r.StatusCode, out)
		}
	}
	_ = io.Discard
	_ = http.StatusOK
}
