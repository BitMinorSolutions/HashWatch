package app

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/web"
)

func TestLoadConfigWifiMigration(t *testing.T) {
	// New install: phones on Wi-Fi off.
	fresh, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if fresh.WifiOn() || fresh.Gentle {
		t.Fatalf("new install: wifi=%v gentle=%v, want both off", fresh.WifiOn(), fresh.Gentle)
	}

	// Existing settings file without the field: keep the old 0.0.0.0 default.
	dir := t.TempDir()
	old := []byte(`{"config_version":2,"miners":[],"port":48412}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	up, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !up.WifiOn() {
		t.Fatal("upgrade of an existing file must keep Allow phones on Wi-Fi on")
	}
	if up.Gentle {
		t.Fatal("gentle must stay off unless it was saved")
	}

	// An explicit off value is preserved.
	off := t.TempDir()
	b := []byte(`{"allow_wifi":false,"gentle":true,"port":48412}`)
	if err := os.WriteFile(filepath.Join(off, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadConfig(off)
	if got.WifiOn() {
		t.Fatal("saved allow_wifi=false was lost")
	}
	if !got.Gentle {
		t.Fatal("saved gentle=true was lost")
	}
}

func TestResolveListenHost(t *testing.T) {
	if h := ResolveListenHost("", false, false); h != "127.0.0.1" {
		t.Fatalf("new default: %s", h)
	}
	if h := ResolveListenHost("", true, false); h != "0.0.0.0" {
		t.Fatalf("wifi on: %s", h)
	}
	if h := ResolveListenHost("0.0.0.0", false, false); h != "0.0.0.0" {
		t.Fatalf("explicit: %s", h)
	}
	if h := ResolveListenHost("0.0.0.0", true, true); h != "127.0.0.1" {
		t.Fatalf("preview must stay on loopback: %s", h)
	}
}

func TestGentleInterval(t *testing.T) {
	p := NewPoller(t.TempDir(), DefaultPoll)
	if p.Every != DefaultPoll || p.Base != DefaultPoll {
		t.Fatalf("base %v %v", p.Every, p.Base)
	}
	p.SetGentle(true)
	if p.Every != GentlePoll {
		t.Fatalf("gentle every %v want %v", p.Every, GentlePoll)
	}
	want := []time.Duration{GentlePoll, GentlePoll, GentlePoll}
	for fails, w := range want {
		if got := p.interval(fails); got != w {
			t.Fatalf("gentle fails=%d: got %v want %v", fails, got, w)
		}
	}
	p.SetGentle(false)
	if p.Every != DefaultPoll || p.interval(0) != DefaultPoll || p.interval(1) != 15*time.Second {
		t.Fatalf("after off: every=%v i0=%v i1=%v", p.Every, p.interval(0), p.interval(1))
	}
	// A slower --poll stays at that rate when gentle is turned on.
	slow := NewPoller(t.TempDir(), time.Minute)
	slow.SetGentle(true)
	if slow.Every != time.Minute {
		t.Fatalf("must not speed up a slower poll: %v", slow.Every)
	}
}

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	st, err := http.NewRequest("GET", req.URL.Scheme+"://"+req.URL.Host+"/api/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := (&http.Client{Timeout: 4 * time.Second}).Do(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range sr.Cookies() {
		if c.Name == csrfCookie && c.MaxAge >= 0 {
			req.AddCookie(c)
			req.Header.Set("X-CSRF-Token", c.Value)
		}
	}
	sr.Body.Close()
	r, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return r.StatusCode, out
}

// Fresh settings + the simulator on temporary ports: default bind is loopback,
// the prefs API flips Wi-Fi access (rebinds) and Gentle mode, and both persist.
func TestWifiAndGentleWithSimulator(t *testing.T) {
	m := sim.New()
	mln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer mln.Close()
	maddr := mln.Addr().String()
	mh, mp, _ := net.SplitHostPort(maddr)
	mport, _ := strconv.Atoi(mp)

	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WifiOn() {
		t.Fatal("fresh dir must not enable Wi-Fi access")
	}
	cfg.Miners = []Miner{{ID: "n3", Name: "Nano 3", Host: mh, Port: mport, Kind: "cgminer"}}
	poll := NewPoller(dir, 200*time.Millisecond)
	poll.Start(cfg.Miners[0])
	defer poll.Stop("n3")

	host := ResolveListenHost("", cfg.WifiOn(), false)
	if host != "127.0.0.1" {
		t.Fatalf("listen host %s", host)
	}
	ln, port, err := Listen(host, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: cfg, Poll: poll, Static: web.FS, InstanceID: NewID(), Port: port, Host: host, LANOK: WildcardHost(host)}
	s.AttachListener(ln, host)
	errCh := make(chan error, 1)
	go func() { errCh <- s.ServeLoop() }()
	t.Cleanup(func() {
		s.lnMu.Lock()
		if s.ln != nil {
			s.ln.Close()
		}
		s.lnMu.Unlock()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	})

	if ip := ln.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
		t.Fatalf("fresh install bound %s, want loopback", ln.Addr())
	}

	base := LocalURL(port)
	// Give the server a moment to accept, and the poller a sample.
	deadline := time.Now().Add(3 * time.Second)
	var st map[string]any
	for time.Now().Before(deadline) {
		body := get(t, base+"/api/state")
		json.Unmarshal([]byte(body), &st)
		if st["allow_wifi"] == false && st["gentle"] == false {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["allow_wifi"] != false || st["gentle"] != false {
		t.Fatalf("state prefs: %v", st)
	}

	// Wait until the simulator has been read at least once.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := poll.Live("n3", 0); ok && v.Latest != nil && v.Latest.OK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v, _ := poll.Live("n3", 0); v.Latest == nil || !v.Latest.OK {
		t.Fatalf("simulator was not read: %+v", v.LastErr)
	}

	code, out := postJSON(t, base+"/api/prefs", `{"allow_wifi":true}`)
	if code != 200 || out["allow_wifi"] != true || out["rebinding"] != true {
		t.Fatalf("enable wifi: %d %v", code, out)
	}
	deadline = time.Now().Add(3 * time.Second)
	var bound net.Addr
	for time.Now().Before(deadline) {
		s.lnMu.Lock()
		ok := s.LANOK && WildcardHost(s.Host)
		if s.ln != nil {
			bound = s.ln.Addr()
		}
		s.lnMu.Unlock()
		if ok && addrUnspecified(bound) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	s.lnMu.Lock()
	if s.ln != nil {
		bound = s.ln.Addr()
	}
	lan, hostNow := s.LANOK, s.Host
	s.lnMu.Unlock()
	if !lan || hostNow != "0.0.0.0" || !addrUnspecified(bound) {
		t.Fatalf("after wifi on: lan=%v host=%s bound=%v", lan, hostNow, bound)
	}
	// Loopback still reaches the dashboard (window + tunnel path).
	if body := get(t, base+"/api/ping"); !strings.Contains(body, s.InstanceID) {
		t.Fatalf("loopback lost after rebind: %s", body)
	}

	code, out = postJSON(t, base+"/api/prefs", `{"gentle":true}`)
	if code != 200 || out["gentle"] != true {
		t.Fatalf("enable gentle: %d %v", code, out)
	}
	if poll.Every != GentlePoll {
		t.Fatalf("poller every %v want %v", poll.Every, GentlePoll)
	}
	if int(out["poll_seconds"].(float64)) != int(GentlePoll/time.Second) {
		t.Fatalf("poll_seconds %v", out["poll_seconds"])
	}

	// Settings survive a reload of the same folder.
	reload, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reload.WifiOn() || !reload.Gentle {
		t.Fatalf("persisted: wifi=%v gentle=%v", reload.WifiOn(), reload.Gentle)
	}

	code, out = postJSON(t, base+"/api/prefs", `{"allow_wifi":false}`)
	if code != 200 || out["allow_wifi"] != false {
		t.Fatalf("disable wifi: %d %v", code, out)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.lnMu.Lock()
		ok := !s.LANOK && s.Host == "127.0.0.1"
		s.lnMu.Unlock()
		if ok {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	s.lnMu.Lock()
	hostNow, lan, bound = s.Host, s.LANOK, nil
	if s.ln != nil {
		bound = s.ln.Addr()
	}
	s.lnMu.Unlock()
	if lan || hostNow != "127.0.0.1" || !addrLoopback(bound) {
		t.Fatalf("after wifi off: lan=%v host=%s bound=%v", lan, hostNow, bound)
	}
	if body := get(t, base+"/api/ping"); !strings.Contains(body, s.InstanceID) {
		t.Fatalf("loopback lost after second rebind: %s", body)
	}
}

func addrUnspecified(a net.Addr) bool {
	ta, ok := a.(*net.TCPAddr)
	return ok && ta.IP.IsUnspecified()
}

func addrLoopback(a net.Addr) bool {
	ta, ok := a.(*net.TCPAddr)
	return ok && ta.IP.IsLoopback()
}
