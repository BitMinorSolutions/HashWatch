package app

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/web"
)

func TestCSRFRequiredOnWrites(t *testing.T) {
	ts, _, _ := secServer(t)
	// A POST without the token is refused.
	req, _ := http.NewRequest("POST", ts.URL+"/api/login", strings.NewReader(`{"password":"mainpass1"}`))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("login without CSRF: %d", r.StatusCode)
	}
	c := &tclient{t: t, ts: ts}
	if r, out := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatalf("login with CSRF: %d %v", r.StatusCode, out)
	}
	// Wrong token is refused even with a session.
	req, _ = http.NewRequest("POST", ts.URL+"/api/prefs", strings.NewReader(`{"gentle":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "not-the-token")
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	if c.csrf != nil {
		req.AddCookie(c.csrf)
	}
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("wrong CSRF: %d", r.StatusCode)
	}
}

func TestSessionLastsAtMost7Days(t *testing.T) {
	ts, s, now := secServer(t)
	c := &tclient{t: t, ts: ts}
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	if r, out := c.do("GET", "/api/state", ""); r.StatusCode != 200 || out["authed"] != true {
		t.Fatalf("authed: %d %v", r.StatusCode, out)
	}
	// 6 days later, still signed in (under the cap, and not idle that long in one jump...
	// we skip 6 days, which also means 6 days idle). 6 < 7, still valid.
	*now = now.Add(6 * 24 * time.Hour)
	s.Now = func() time.Time { return *now }
	if r, out := c.do("GET", "/api/state", ""); r.StatusCode != 200 || out["authed"] != true {
		t.Fatalf("6 days: %v", out)
	}
	// One more day exceeds the 7-day cap (and idle).
	*now = now.Add(24 * time.Hour)
	s.Now = func() time.Time { return *now }
	if r, out := c.do("GET", "/api/state", ""); r.StatusCode != 200 || out["authed"] != false {
		t.Fatalf("after 7 days should be signed out: %v", out)
	}
}

func TestMinerWriteRePromptsAdminPassword(t *testing.T) {
	m := sim.New()
	ln, _ := m.Listen("127.0.0.1:0")
	defer ln.Close()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	ts, s := newTestServer(t, []Miner{{ID: "d1", Name: "sim", Host: h, Port: port, Kind: "cgminer"}}, false, "secret123")
	c := &tclient{t: t, ts: ts}
	if r, _ := c.do("POST", "/api/login", `{"password":"secret123"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	if r, out := c.do("POST", "/api/control/workmode", `{"miner":"d1","mode":"low"}`); r.StatusCode != 403 || !strings.Contains(fmtErr(out), "admin password") {
		t.Fatalf("missing password: %d %v", r.StatusCode, out)
	}
	if r, out := c.do("POST", "/api/control/workmode", `{"miner":"d1","mode":"low","password":"wrong"}`); r.StatusCode != 403 {
		t.Fatalf("wrong password: %d %v", r.StatusCode, out)
	}
	if r, out := c.do("POST", "/api/control/workmode", `{"miner":"d1","mode":"low","password":"secret123"}`); r.StatusCode != 200 {
		t.Fatalf("admin password: %d %v", r.StatusCode, out)
	}
	_ = s
	if m.WorkLevel != 0 {
		t.Fatalf("work level %d", m.WorkLevel)
	}
}

func TestNoPasswordBlocksNonLocalhostWrites(t *testing.T) {
	cfg, _ := LoadConfig(t.TempDir())
	on := true
	cfg.AllowWifi = &on
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, LANOK: true, InstanceID: NewID()}
	ts := newTestServerFrom(t, s)
	c := &tclient{t: t, ts: ts}
	_, st := c.do("GET", "/api/state", "")
	if st["wifi_no_password"] != true || st["auth_required"] != false {
		t.Fatalf("warning flag: %v", st)
	}
	// Loopback can still change settings when no password is set.
	if r, out := c.do("POST", "/api/prefs", `{"gentle":true}`); r.StatusCode != 200 {
		t.Fatalf("localhost write: %d %v", r.StatusCode, out)
	}
	// A phone on the LAN (not this computer, not the Cloudflare tunnel) is
	// refused until a password is set.
	c.grabCSRF()
	req, _ := http.NewRequest("POST", "/api/prefs", strings.NewReader(`{"gentle":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.tok)
	if c.csrf != nil {
		req.AddCookie(c.csrf)
	}
	req.RemoteAddr = "192.168.1.50:4321"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	res := w.Result()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	if res.StatusCode != 403 || !strings.Contains(fmtErr(out), "password") {
		t.Fatalf("non-local write: %d %v", res.StatusCode, out)
	}
}

func newTestServerFrom(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	if s.Static == nil {
		s.Static = web.FS
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestStateIncludesCSRF(t *testing.T) {
	ts, _, _ := secServer(t)
	r, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	if out["csrf"] == nil || out["csrf"] == "" {
		t.Fatalf("state missing csrf: %v", out)
	}
	var gotCookie bool
	for _, c := range r.Cookies() {
		if c.Name == csrfCookie && c.Value != "" {
			gotCookie = true
		}
	}
	if !gotCookie {
		t.Fatal("missing hw_csrf cookie")
	}
}
