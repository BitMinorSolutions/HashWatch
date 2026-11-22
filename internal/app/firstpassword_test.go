package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

// firstPwServer is a fresh install: no password, no miners.
func firstPwServer(t *testing.T) (*Server, *tclient) {
	t.Helper()
	cfg, _ := LoadConfig(t.TempDir())
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, LANOK: true, InstanceID: NewID()}
	ts := newTestServerFrom(t, s)
	c := &tclient{t: t, ts: ts}
	c.grabCSRF()
	return s, c
}

// as sends a request straight to the handler as if it came from addr.
func (c *tclient) as(s *Server, addr, method, path, body string, hdr map[string]string) (*http.Response, map[string]any) {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = addr
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-CSRF-Token", c.tok)
	req.AddCookie(c.csrf)
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	res := w.Result()
	for _, k := range res.Cookies() {
		if k.Name == "hw_session" && k.MaxAge >= 0 {
			c.ck = k
		}
	}
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	return res, out
}

func TestFirstPasswordFromLAN(t *testing.T) {
	for _, addr := range []string{"192.168.1.50:4321", "10.0.0.9:1", "169.254.3.4:1", "100.121.124.114:5555", "[fe80::1]:1"} {
		s, c := firstPwServer(t)
		_, st := c.as(s, addr, "GET", "/api/state", "", nil)
		if st["password_first"] != true || st["password_first_ok"] != true {
			t.Fatalf("%s state: %v", addr, st)
		}
		if r, out := c.as(s, addr, "POST", "/api/password", `{"current":"","new":"hunter22"}`, nil); r.StatusCode != 200 {
			t.Fatalf("%s first set: %d %v", addr, r.StatusCode, out)
		}
		if !s.Cfg.CheckPassword("hunter22") || s.Cfg.PasswordHash == "" {
			t.Fatalf("%s password not saved", addr)
		}
		// The same device stays signed in: the next state is authed and still in setup.
		_, st = c.as(s, addr, "GET", "/api/state", "", nil)
		if st["authed"] != true || st["setup_needed"] != true || st["password_first"] != nil {
			t.Fatalf("%s after set: %v", addr, st)
		}
	}
}

func TestFirstPasswordThenMinerWriteFromLAN(t *testing.T) {
	s, c := firstPwServer(t)
	addr := "192.168.1.50:4321"
	if r, out := c.as(s, addr, "POST", "/api/prefs", `{"gentle":true}`, nil); r.StatusCode != 403 {
		t.Fatalf("write before password: %d %v", r.StatusCode, out)
	}
	if r, out := c.as(s, addr, "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 200 {
		t.Fatalf("first set: %d %v", r.StatusCode, out)
	}
	if r, out := c.as(s, addr, "POST", "/api/prefs", `{"gentle":true}`, nil); r.StatusCode != 200 {
		t.Fatalf("write after password: %d %v", r.StatusCode, out)
	}
}

func TestFirstPasswordRejectedFromPublicIP(t *testing.T) {
	for _, addr := range []string{"8.8.8.8:1", "100.128.0.1:1", "[2001:4860::8888]:1"} {
		s, c := firstPwServer(t)
		_, st := c.as(s, addr, "GET", "/api/state", "", nil)
		if st["password_first_ok"] != false {
			t.Fatalf("%s state: %v", addr, st)
		}
		if r, out := c.as(s, addr, "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 403 {
			t.Fatalf("%s: %d %v", addr, r.StatusCode, out)
		}
		if s.Cfg.PasswordHash != "" {
			t.Fatalf("%s set a password", addr)
		}
	}
}

func TestFirstPasswordRejectedThroughTunnel(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:5000", "192.168.1.50:1"} {
		s, c := firstPwServer(t)
		h := map[string]string{"Cf-Ray": "x", "Cf-Connecting-Ip": "192.168.1.9"}
		if r, _ := c.as(s, addr, "POST", "/api/password", `{"new":"hunter22"}`, h); r.StatusCode == 200 {
			t.Fatalf("%s tunnel set a password", addr)
		}
		if s.Cfg.PasswordHash != "" {
			t.Fatalf("%s tunnel set a password", addr)
		}
	}
}

func TestSecondUnauthenticatedSetRejected(t *testing.T) {
	s, a := firstPwServer(t)
	if r, out := a.as(s, "192.168.1.50:1", "POST", "/api/password", `{"new":"first-one"}`, nil); r.StatusCode != 200 {
		t.Fatalf("first: %d %v", r.StatusCode, out)
	}
	// Another device, no session, no current password.
	b := &tclient{t: t, ts: a.ts, csrf: a.csrf, tok: a.tok}
	if r, out := b.as(s, "192.168.1.77:1", "POST", "/api/password", `{"new":"second-one"}`, nil); r.StatusCode != 401 {
		t.Fatalf("second: %d %v", r.StatusCode, out)
	}
	if !s.Cfg.CheckPassword("first-one") {
		t.Fatal("first password was replaced")
	}
	// Even signed in, changing it still needs the current password.
	if r, out := a.as(s, "192.168.1.50:1", "POST", "/api/password", `{"current":"nope","new":"third-one"}`, nil); r.StatusCode != 401 {
		t.Fatalf("change without current: %d %v", r.StatusCode, out)
	}
	if r, out := a.as(s, "192.168.1.50:1", "POST", "/api/password", `{"current":"first-one","new":"third-one"}`, nil); r.StatusCode != 200 {
		t.Fatalf("change with current: %d %v", r.StatusCode, out)
	}
}

func TestFirstPasswordRace(t *testing.T) {
	s, c := firstPwServer(t)
	const n = 20
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cl := &tclient{t: t, ts: c.ts, csrf: c.csrf, tok: c.tok}
			r, _ := cl.as(s, "192.168.1.50:1", "POST", "/api/password", `{"new":"racer-`+string(rune('a'+i))+`"}`, nil)
			codes[i] = r.StatusCode
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, code := range codes {
		if code == 200 {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d first-time sets won: %v", wins, codes)
	}
}

func TestFirstPasswordKeepsCSRF(t *testing.T) {
	s, c := firstPwServer(t)
	c.tok = "wrong"
	if r, _ := c.as(s, "192.168.1.50:1", "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 403 {
		t.Fatalf("bad csrf: %d", r.StatusCode)
	}
	c.tok = c.csrf.Value
	if r, _ := c.as(s, "192.168.1.50:1", "POST", "/api/password", `{"new":"hunter22"}`, map[string]string{"Sec-Fetch-Site": "cross-site"}); r.StatusCode != 403 {
		t.Fatalf("cross-site: %d", r.StatusCode)
	}
	if s.Cfg.PasswordHash != "" {
		t.Fatal("password set despite failed checks")
	}
}

func TestOtherWritesStillBlockedWithoutPassword(t *testing.T) {
	s, c := firstPwServer(t)
	for _, p := range []string{"/api/miners", "/api/discover", "/api/prefs"} {
		if r, out := c.as(s, "100.121.124.114:1", "POST", p, `{}`, nil); r.StatusCode != 403 {
			t.Fatalf("%s: %d %v", p, r.StatusCode, out)
		}
	}
}
