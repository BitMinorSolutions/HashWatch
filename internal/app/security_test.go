package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

type tclient struct {
	t    *testing.T
	ts   *httptest.Server
	ck   *http.Cookie
	csrf *http.Cookie
	tok  string
	ip   string // when set, the request looks like it came through the tunnel from this address
}

func (c *tclient) grabCSRF() {
	c.t.Helper()
	if c.tok != "" && c.csrf != nil {
		return
	}
	req, _ := http.NewRequest("GET", c.ts.URL+"/api/state", nil)
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	if c.csrf != nil {
		req.AddCookie(c.csrf)
	}
	if c.ip != "" {
		req.Header.Set("Cf-Ray", "test")
		req.Header.Set("Cf-Connecting-Ip", c.ip)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer r.Body.Close()
	c.takeCookies(r)
}

func (c *tclient) takeCookies(r *http.Response) {
	for _, k := range r.Cookies() {
		switch {
		case k.Name == "hw_session" && k.MaxAge >= 0:
			c.ck = k
		case k.Name == csrfCookie && k.MaxAge >= 0:
			c.csrf, c.tok = k, k.Value
		}
	}
}

func (c *tclient) do(method, path, body string) (*http.Response, map[string]any) {
	if method != "GET" && method != "HEAD" {
		c.grabCSRF()
	}
	req, _ := http.NewRequest(method, c.ts.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && method != "HEAD" && c.tok != "" {
		req.Header.Set("X-CSRF-Token", c.tok)
	}
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	if c.csrf != nil {
		req.AddCookie(c.csrf)
	}
	if c.ip != "" {
		req.Header.Set("Cf-Ray", "test")
		req.Header.Set("Cf-Connecting-Ip", c.ip)
	}
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := cl.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	c.takeCookies(r)
	return r, out
}

// attachCSRF adds a valid anti-forgery cookie and header to a raw request.
func attachCSRF(t *testing.T, ts *httptest.Server, req *http.Request) {
	t.Helper()
	c := &tclient{t: t, ts: ts}
	c.grabCSRF()
	req.Header.Set("X-CSRF-Token", c.tok)
	if c.csrf != nil {
		req.AddCookie(c.csrf)
	}
}

func secServer(t *testing.T) (*httptest.Server, *Server, *time.Time) {
	cfg, _ := LoadConfig(t.TempDir())
	cfg.SetPassword("mainpass1")
	now := time.Unix(1794642000, 0)
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Now: func() time.Time { return now }, Alerts: NewAlerts(t.TempDir())}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s, &now
}

// shareOn turns Share on directly (no cloudflared) and returns the link path.
func shareOn(s *Server) string {
	s.Cfg.Remote.Enabled = true
	s.EnsureShareLink()
	return "/v/" + s.Cfg.Security.ViewLink
}

// Wrong share-link tokens through the tunnel lock that address out.
func TestLockoutBackoff(t *testing.T) {
	ts, s, now := secServer(t)
	good := shareOn(s)
	c := &tclient{t: t, ts: ts, ip: "203.0.113.5"}
	for i := 1; i <= freeTries; i++ {
		if r, _ := c.do("GET", "/v/wrong", ""); r.StatusCode != 404 {
			t.Fatalf("try %d: %d", i, r.StatusCode)
		}
	}
	r, _ := c.do("GET", "/v/wrong", "")
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("after %d wrong tries the address is locked: %d", freeTries, r.StatusCode)
	}
	// locked: even the right link is refused until the lock ends
	if r, _ := c.do("GET", good, ""); r.StatusCode != 429 {
		t.Fatal("locked out")
	}
	// another address isn't affected
	other := &tclient{t: t, ts: ts, ip: "198.51.100.7"}
	if r, _ := other.do("GET", good, ""); r.StatusCode != 303 {
		t.Fatal("other address should get in")
	}
	*now = now.Add(61 * time.Second)
	// one more wrong try doubles the lock (2 min)
	c.do("GET", "/v/wrong", "")
	*now = now.Add(70 * time.Second)
	if r, _ := c.do("GET", good, ""); r.StatusCode != 429 {
		t.Fatal("second lock should be 2 minutes")
	}
	*now = now.Add(60 * time.Second)
	if r, _ := c.do("GET", good, ""); r.StatusCode != 303 {
		t.Fatal("after the lock the right link works")
	}
	// home-network password sign-in has its own lockout
	home := &tclient{t: t, ts: ts}
	for i := 0; i < freeTries; i++ {
		home.do("POST", "/api/login", `{"password":"nope"}`)
	}
	if r, _ := home.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 429 {
		t.Fatalf("home lockout: %d", r.StatusCode)
	}
}

func TestGlobalRemoteLimit(t *testing.T) {
	ts, s, _ := secServer(t)
	good := shareOn(s)
	for i := 0; i < globalMax; i++ {
		c := &tclient{t: t, ts: ts, ip: "203.0.113." + itoa(i+1)}
		c.do("GET", "/v/wrong", "")
	}
	fresh := &tclient{t: t, ts: ts, ip: "192.0.2.200"}
	if r, _ := fresh.do("GET", good, ""); r.StatusCode != 429 {
		t.Fatalf("share link should pause after %d failures from many addresses: %d", globalMax, r.StatusCode)
	}
	home := &tclient{t: t, ts: ts}
	if r, _ := home.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("home network sign-in keeps working")
	}
}

func TestNoPasswordNoRemote(t *testing.T) {
	ts, s, _ := secServer(t)
	s.Cfg.SetPassword("")
	home := &tclient{t: t, ts: ts}
	if r, _ := home.do("GET", "/api/alerts", ""); r.StatusCode == 401 {
		t.Fatal("home without password is open")
	}
	far := &tclient{t: t, ts: ts, ip: "203.0.113.9"}
	if r, _ := far.do("GET", "/api/lottery?miner=x", ""); r.StatusCode != 401 {
		t.Fatalf("remote without a password must be refused: %d", r.StatusCode)
	}
}

func TestViewOnlyRole(t *testing.T) {
	ts, s, _ := secServer(t)
	admin := &tclient{t: t, ts: ts}
	admin.do("POST", "/api/login", `{"password":"mainpass1"}`)
	if r, _ := admin.do("POST", "/api/security/view", `{"password":"mainpass1"}`); r.StatusCode != 400 {
		t.Fatal("view password must differ from the main one")
	}
	if r, _ := admin.do("POST", "/api/security/view", `{"password":"lookonly"}`); r.StatusCode != 200 {
		t.Fatal("set view password")
	}
	viewer := &tclient{t: t, ts: ts} // view password: home network only
	r, out := viewer.do("POST", "/api/login", `{"password":"lookonly"}`)
	if r.StatusCode != 200 || out["role"] != RoleView {
		t.Fatalf("view login: %d %v", r.StatusCode, out)
	}
	_, st := viewer.do("GET", "/api/state", "")
	if st["view_only"] != true || st["remote"] != nil {
		t.Fatalf("state for viewer: %v", st)
	}
	// server-enforced: every control and setting is 403 for the viewer
	for _, c := range [][3]string{
		{"POST", "/api/control/workmode", `{"miner":"m","mode":"low"}`},
		{"POST", "/api/control/led", `{"miner":"m","mode":"fixed","color":"#ff0000","brightness":50}`},
		{"POST", "/api/control/fan", `{"miner":"m","mode":"cooler","percent":50}`},
		{"POST", "/api/features", `{"miner":"m","fun":{"summary":"off"}}`},
		{"POST", "/api/features", `{"miner":"m","failover":{"on":true,"dead_min":5}}`},
		{"POST", "/api/prefs", `{"allow_wifi":true,"gentle":true}`},
		{"POST", "/api/control/pool", `{"miner":"m","pool":0,"url":"stratum+tcp://example.com:3333","worker":"w","worker_pass":"x","password":"lookonly"}`},
		{"POST", "/api/control/reboot", `{"miner":"m","password":"lookonly"}`},
		{"GET", "/api/update", ""},
		{"POST", "/api/update", `{"apply":true}`},
		{"GET", "/api/tailscale", ""},
		{"POST", "/api/tailscale/serve", `{"enabled":true}`},
		{"GET", "/api/ntfy", ""},
		{"POST", "/api/ntfy", `{"enabled":true}`},
		{"POST", "/api/ntfy/test", `{}`},
		{"POST", "/api/alerts/clear", `{}`},
		{"POST", "/api/sessions/revoke", `{}`},
		{"GET", "/api/remote", ""},
		{"GET", "/api/debug", ""},
		{"GET", "/api/security", ""},
	} {
		if r, _ := viewer.do(c[0], c[1], c[2]); r.StatusCode != 403 {
			t.Errorf("%s %s: viewer got %d, want 403", c[0], c[1], r.StatusCode)
		}
	}
	// the view password doesn't work through the share link
	far := &tclient{t: t, ts: ts, ip: "203.0.113.30"}
	if r, _ := far.do("POST", "/api/login", `{"password":"lookonly"}`); r.StatusCode != 403 {
		t.Fatalf("view password over the tunnel: %d", r.StatusCode)
	}
	_ = s
}

func TestSignOutEverywhere(t *testing.T) {
	ts, _, _ := secServer(t)
	a := &tclient{t: t, ts: ts}
	b := &tclient{t: t, ts: ts}
	c := &tclient{t: t, ts: ts}
	for _, x := range []*tclient{a, b, c} {
		if r, _ := x.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
			t.Fatal("login")
		}
	}
	_, rv := a.do("POST", "/api/sessions/revoke", "{}")
	if rv["signed_out"].(float64) != 2 {
		t.Fatalf("revoke: %v", rv)
	}
	for _, x := range []*tclient{b, c} {
		if r, _ := x.do("GET", "/api/alerts", ""); r.StatusCode != 401 {
			t.Fatal("everyone else signed out")
		}
	}
	if r, _ := a.do("GET", "/api/alerts", ""); r.StatusCode != 200 {
		t.Fatal("the asking device stays signed in")
	}
	// removed features stay removed
	for _, p := range [][2]string{{"POST", "/api/2fa/setup"}, {"GET", "/api/sessions"}} {
		if r, _ := a.do(p[0], p[1], "{}"); r.StatusCode != 404 && r.StatusCode != 405 {
			t.Errorf("%v still answers: %d", p, r.StatusCode)
		}
	}
}

func TestConfigMigration(t *testing.T) {
	dir := t.TempDir()
	old := `{"miners":[{"id":"abc","name":"Nano","host":"192.168.1.50","port":4028,"kind":"cgminer"}],"password_hash":"x","password_salt":"00","remote":{"enabled":true,"last_url":"https://a.trycloudflare.com"},"alerts_off":{"temp":true},"port":48412}`
	if err := writeFile(dir+"/config.json", old); err != nil {
		t.Fatal(err)
	}
	c, _ := LoadConfig(dir)
	if len(c.Miners) != 1 || c.Miners[0].Host != "192.168.1.50" || !c.Remote.Enabled || c.Port != 48412 || !c.AlertsOff["temp"] || c.PasswordHash != "x" {
		t.Fatalf("old settings lost: %+v", c.ConfigData)
	}
	// new features are all off by default, live data on
	o := c.Miners[0].Opts
	if o.Schedule.On || o.Failover.On || o.Light.Lamp || o.Light.Celebrate || o.Light.BestFlash {
		t.Fatal("automations must default to off")
	}
	if c.Privacy.NetworkOff || c.Security.ViewLink != "" {
		t.Fatal("defaults")
	}
	c.Save()
	c2, _ := LoadConfig(dir)
	if c2.Miners[0].ID != "abc" || c2.Version != ConfigVersion {
		t.Fatal("round trip")
	}
}
