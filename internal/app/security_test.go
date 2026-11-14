package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/totp"
	"github.com/hashwatch/hashwatch/web"
)

type tclient struct {
	t  *testing.T
	ts *httptest.Server
	ck *http.Cookie
	ip string // when set, the request looks like it came through the tunnel from this address
}

func (c *tclient) do(method, path, body string) (*http.Response, map[string]any) {
	req, _ := http.NewRequest(method, c.ts.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.ck != nil {
		req.AddCookie(c.ck)
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
	for _, k := range r.Cookies() {
		if k.Name == "hw_session" && k.MaxAge >= 0 {
			c.ck = k
		}
	}
	return r, out
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

func TestLockoutBackoff(t *testing.T) {
	ts, _, now := secServer(t)
	c := &tclient{t: t, ts: ts, ip: "203.0.113.5"}
	for i := 1; i < freeTries; i++ {
		if r, _ := c.do("POST", "/api/login", `{"password":"nope"}`); r.StatusCode != 401 {
			t.Fatalf("try %d: %d", i, r.StatusCode)
		}
	}
	r, out := c.do("POST", "/api/login", `{"password":"nope"}`)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("5th wrong try should lock: %d %v", r.StatusCode, out)
	}
	// locked: even the right password is refused until the lock ends
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 429 {
		t.Fatal("locked out")
	}
	// another address isn't affected
	other := &tclient{t: t, ts: ts, ip: "198.51.100.7"}
	if r, _ := other.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("other address should sign in")
	}
	*now = now.Add(61 * time.Second)
	// one more wrong try doubles the lock (2 min)
	c.do("POST", "/api/login", `{"password":"nope"}`)
	*now = now.Add(70 * time.Second)
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 429 {
		t.Fatal("second lock should be 2 minutes")
	}
	*now = now.Add(60 * time.Second)
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("after the lock the right password works")
	}
}

func TestGlobalRemoteLimit(t *testing.T) {
	ts, _, _ := secServer(t)
	for i := 0; i < globalMax; i++ {
		c := &tclient{t: t, ts: ts, ip: "203.0.113." + itoa(i+1)}
		c.do("POST", "/api/login", `{"password":"nope"}`)
	}
	fresh := &tclient{t: t, ts: ts, ip: "192.0.2.200"}
	if r, _ := fresh.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 429 {
		t.Fatalf("remote sign-ins should pause after %d failures from many addresses: %d", globalMax, r.StatusCode)
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

func TestTOTPFlow(t *testing.T) {
	ts, s, now := secServer(t)
	home := &tclient{t: t, ts: ts}
	if r, _ := home.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	_, setup := home.do("POST", "/api/2fa/setup", "{}")
	sec, _ := setup["secret"].(string)
	if sec == "" || !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/") || !strings.Contains(setup["qr_svg"].(string), "<svg") {
		t.Fatalf("setup: %v", setup)
	}
	if r, _ := home.do("POST", "/api/2fa/enable", `{"code":"000000"}`); r.StatusCode != 400 {
		t.Fatal("wrong code must not enable")
	}
	code, _ := totp.Code(sec, totp.Counter(*now))
	r, en := home.do("POST", "/api/2fa/enable", `{"code":"`+code+`"}`)
	rec, _ := en["recovery"].([]any)
	if r.StatusCode != 200 || len(rec) != 10 || !s.Cfg.Security.TOTPOn {
		t.Fatalf("enable: %d %v", r.StatusCode, en)
	}
	// remote sign-in now needs the code
	far := &tclient{t: t, ts: ts, ip: "203.0.113.20"}
	r, out := far.do("POST", "/api/login", `{"password":"mainpass1"}`)
	if r.StatusCode != 401 || out["need_code"] != true {
		t.Fatalf("want need_code: %d %v", r.StatusCode, out)
	}
	if r, _ := far.do("POST", "/api/login", `{"password":"mainpass1","code":"`+code+`"}`); r.StatusCode != 401 {
		t.Fatal("the code used to enable can't be replayed")
	}
	*now = now.Add(30 * time.Second)
	code2, _ := totp.Code(sec, totp.Counter(*now))
	if r, _ := far.do("POST", "/api/login", `{"password":"mainpass1","code":"`+code2+`"}`); r.StatusCode != 200 {
		t.Fatal("fresh code signs in")
	}
	// recovery code works once
	far2 := &tclient{t: t, ts: ts, ip: "203.0.113.21"}
	rc := rec[0].(string)
	if r, _ := far2.do("POST", "/api/login", `{"password":"mainpass1","code":"`+rc+`"}`); r.StatusCode != 200 {
		t.Fatal("recovery code")
	}
	far3 := &tclient{t: t, ts: ts, ip: "203.0.113.22"}
	if r, _ := far3.do("POST", "/api/login", `{"password":"mainpass1","code":"`+rc+`"}`); r.StatusCode != 401 {
		t.Fatal("recovery code is single-use")
	}
	// home network: no code unless "also at home" is on
	home2 := &tclient{t: t, ts: ts}
	if r, _ := home2.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("home sign-in without code")
	}
	// disabling needs the password
	if r, _ := home.do("POST", "/api/2fa/disable", `{"password":"bad"}`); r.StatusCode != 401 {
		t.Fatal("disable needs password")
	}
	if r, _ := home.do("POST", "/api/2fa/disable", `{"password":"mainpass1"}`); r.StatusCode != 200 || s.Cfg.Security.TOTPOn {
		t.Fatal("disable")
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
	viewer := &tclient{t: t, ts: ts, ip: "203.0.113.30"}
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
		{"POST", "/api/features", `{"miner":"m","fun":{"summary":"off"}}`},
		{"POST", "/api/alerts/clear", `{}`},
		{"POST", "/api/sessions/revoke", `{}`},
		{"GET", "/api/sessions", ""},
		{"GET", "/api/remote", ""},
		{"GET", "/api/debug", ""},
		{"GET", "/api/security", ""},
	} {
		if r, _ := viewer.do(c[0], c[1], c[2]); r.StatusCode != 403 {
			t.Errorf("%s %s: viewer got %d, want 403", c[0], c[1], r.StatusCode)
		}
	}
	// view-only link
	_, sec := admin.do("POST", "/api/security/viewlink", `{"action":"create"}`)
	path, _ := sec["view_link_path"].(string)
	if !strings.HasPrefix(path, "/v/") {
		t.Fatalf("link: %v", sec)
	}
	guest := &tclient{t: t, ts: ts, ip: "203.0.113.31"}
	if r, _ := guest.do("GET", path, ""); r.StatusCode != 303 {
		t.Fatal("link signs in and redirects")
	}
	if _, st := guest.do("GET", "/api/state", ""); st["role"] != RoleView {
		t.Fatal("link gives view role")
	}
	if r, _ := guest.do("POST", "/api/control/workmode", `{"miner":"m","mode":"low"}`); r.StatusCode != 403 {
		t.Fatal("link viewer can't control")
	}
	// rotating the link signs out its viewers and kills the old link
	admin.do("POST", "/api/security/viewlink", `{"action":"rotate"}`)
	if r, _ := guest.do("GET", "/api/state", ""); r.StatusCode != 200 {
		t.Fatal("state")
	}
	if _, st := guest.do("GET", "/api/state", ""); st["authed"] == true {
		t.Fatal("rotated link: old viewers signed out")
	}
	g2 := &tclient{t: t, ts: ts, ip: "203.0.113.32"}
	if r, _ := g2.do("GET", path, ""); r.StatusCode != 404 {
		t.Fatal("old link is dead")
	}
	_ = s
}

func TestSessionsKickAndRevoke(t *testing.T) {
	ts, _, _ := secServer(t)
	a := &tclient{t: t, ts: ts}
	b := &tclient{t: t, ts: ts, ip: "203.0.113.40"}
	c := &tclient{t: t, ts: ts, ip: "203.0.113.41"}
	for _, x := range []*tclient{a, b, c} {
		if r, _ := x.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
			t.Fatal("login")
		}
	}
	_, list := a.do("GET", "/api/sessions", "")
	ss := list["sessions"].([]any)
	if len(ss) != 3 {
		t.Fatalf("want 3 sessions: %v", list)
	}
	var bid string
	for _, x := range ss {
		m := x.(map[string]any)
		if m["ip"] == "203.0.113.40" {
			bid = m["id"].(string)
			if m["remote"] != true {
				t.Fatal("tunnel session marked remote")
			}
		}
	}
	if r, _ := a.do("DELETE", "/api/sessions/"+bid, "{}"); r.StatusCode != 200 {
		t.Fatal("kick")
	}
	if r, _ := b.do("GET", "/api/alerts", ""); r.StatusCode != 401 {
		t.Fatal("kicked device is signed out")
	}
	_, rv := a.do("POST", "/api/sessions/revoke", "{}")
	if rv["signed_out"].(float64) != 1 {
		t.Fatalf("revoke: %v", rv)
	}
	if r, _ := c.do("GET", "/api/alerts", ""); r.StatusCode != 401 {
		t.Fatal("everyone else signed out")
	}
	if r, _ := a.do("GET", "/api/sessions", ""); r.StatusCode != 200 {
		t.Fatal("the asking device stays signed in")
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
	if o.Schedule.On || o.Throttle.On || o.Failover.On || o.Restart.On || o.Light.Lamp || o.Light.Celebrate || o.Light.BestFlash {
		t.Fatal("automations must default to off")
	}
	if c.Privacy.NetworkOff || c.Security.TOTPOn || c.Security.ViewLink != "" {
		t.Fatal("defaults")
	}
	c.Save()
	c2, _ := LoadConfig(dir)
	if c2.Miners[0].ID != "abc" || c2.Version != ConfigVersion {
		t.Fatal("round trip")
	}
}
