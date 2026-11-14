package app

import (
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

func newTestServer(t *testing.T, miners []Miner, locked bool, pw string) (*httptest.Server, *Server) {
	cfg, _ := LoadConfig(t.TempDir())
	cfg.Miners = miners
	cfg.SetPassword(pw)
	p := NewPoller(t.TempDir(), time.Hour)
	for _, m := range miners {
		p.Start(m)
	}
	s := &Server{Cfg: cfg, Poll: p, Static: web.FS, Locked: locked}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	time.Sleep(300 * time.Millisecond)
	return ts, s
}

func post(t *testing.T, ts *httptest.Server, path, body string, cookie *http.Cookie) *http.Response {
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	attachCSRF(t, ts, req)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r
}

func TestControlGuards(t *testing.T) {
	m := sim.New()
	ln, _ := m.Listen("127.0.0.1:0")
	defer ln.Close()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	direct := Miner{ID: "d1", Name: "sim", Host: h, Port: port, Kind: "cgminer"}
	remote := Miner{ID: "r1", Name: "remote", Kind: "remote", RemoteURL: "http://127.0.0.1:1"}

	// preview/locked mode: always refused
	ts, _ := newTestServer(t, []Miner{direct}, true, "")
	if r := post(t, ts, "/api/control/workmode", `{"miner":"d1","mode":"low"}`, nil); r.StatusCode != 403 {
		t.Fatalf("locked: %d", r.StatusCode)
	}
	// remote source: refused even when unlocked
	ts, _ = newTestServer(t, []Miner{remote, direct}, false, "")
	if r := post(t, ts, "/api/control/led", `{"miner":"r1","mode":"fixed","color":"#ffffff","brightness":50}`, nil); r.StatusCode != 403 {
		t.Fatalf("remote: %d", r.StatusCode)
	}
	// password set: needs a session
	ts, _ = newTestServer(t, []Miner{direct}, false, "secret123")
	if r := post(t, ts, "/api/control/workmode", `{"miner":"d1","mode":"low"}`, nil); r.StatusCode != 401 {
		t.Fatalf("no session: %d", r.StatusCode)
	}
	lr := post(t, ts, "/api/login", `{"password":"secret123"}`, nil)
	var ck *http.Cookie
	for _, c := range lr.Cookies() {
		if c.Name == "hw_session" {
			ck = c
		}
	}
	if r := post(t, ts, "/api/control/workmode", `{"miner":"d1","mode":"low","password":"secret123"}`, ck); r.StatusCode != 200 {
		t.Fatalf("signed in: %d", r.StatusCode)
	}
	if m.WorkLevel != 0 {
		t.Fatalf("sim work level %d", m.WorkLevel)
	}
	for _, c := range m.Log() {
		if strings.HasPrefix(c, "ascset") && c != "ascset|0,workmode,set,0" {
			t.Fatalf("unexpected write %s", c)
		}
	}
}
