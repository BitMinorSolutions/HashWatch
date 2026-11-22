package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

// connCounter is a fake miner API port that only counts connections.
func connCounter(t *testing.T) (Miner, *atomic.Int64) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var n atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			c.Close()
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return Miner{ID: "m1", Name: "Nano", Host: "127.0.0.1", Port: port, Kind: "cgminer"}, &n
}

// fakePi is a real HashWatch server, as the Raspberry Pi would run it.
func fakePi(t *testing.T) string {
	cfg, _ := LoadConfig(t.TempDir())
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Version: "0.7.2"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

type opened struct {
	mu   sync.Mutex
	urls []string
}

func (o *opened) link() *PiLink {
	return &PiLink{Open: func(u string) { o.mu.Lock(); o.urls = append(o.urls, u); o.mu.Unlock() }}
}
func (o *opened) last() (string, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.urls) == 0 {
		return "", 0
	}
	return o.urls[len(o.urls)-1], len(o.urls)
}

func TestNormalizePiURL(t *testing.T) {
	for in, want := range map[string]string{
		"hashwatch.local:48412":            "http://hashwatch.local:48412",
		" http://192.168.1.212:48412/ ":    "http://192.168.1.212:48412",
		"http://100.121.124.114:48412/#/x": "http://100.121.124.114:48412",
		"https://hashwatch.tail1.ts.net/":  "https://hashwatch.tail1.ts.net",
	} {
		if got, err := NormalizePiURL(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "ftp://x", "http://", "http://u:p@host"} {
		if _, err := NormalizePiURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTestPi(t *testing.T) {
	info, err := TestPi(fakePi(t))
	if err != nil || info.App != AppName || info.Version != "0.7.2" {
		t.Fatalf("real HashWatch: %+v %v", info, err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"app":"SomethingElse"}`))
	}))
	defer other.Close()
	if _, err := TestPi(other.URL); err == nil {
		t.Fatal("a server that isn't HashWatch passed the test")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	if _, err := TestPi(dead); err == nil {
		t.Fatal("an unreachable address passed the test")
	}
}

// Showing a Pi: StartMiners must not open a single connection to the miner.
func TestPiModeStartsNoMinerPolling(t *testing.T) {
	m, n := connCounter(t)
	cfg, _ := LoadConfig(t.TempDir())
	cfg.Miners = []Miner{m}
	cfg.PiMode, cfg.PiURL = true, "http://hashwatch.local:48412"
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), 2*time.Second), Static: web.FS, Pi: &PiLink{}}
	s.StartMiners()
	time.Sleep(2500 * time.Millisecond)
	if got := n.Load(); got != 0 || s.Poll.Running() != 0 {
		t.Fatalf("Pi mode: %d miner connections, %d pollers (want 0, 0)", got, s.Poll.Running())
	}
	// Control: the same config without the Mac window (Pi service, browser) reads the miner.
	s.Pi = nil
	s.StartMiners()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n.Load() == 0 {
		t.Fatal("control: running here should read the miner")
	}
	s.Poll.Halt(m.ID)
}

func TestPiConnectAndSwitchBack(t *testing.T) {
	m, n := connCounter(t)
	dir := t.TempDir()
	cfg, _ := LoadConfig(dir)
	cfg.Miners = []Miner{m}
	var o opened
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), 2*time.Second), Static: web.FS, Pi: o.link()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	s.StartMiners()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n.Load() == 0 {
		t.Fatal("running here should read the miner")
	}
	pi := fakePi(t)

	// /api/state tells this Mac's window about the feature.
	var st struct {
		Pi map[string]any `json:"pi"`
	}
	r, _ := http.Get(ts.URL + "/api/state")
	json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.Pi == nil || st.Pi["mode"] != false {
		t.Fatalf("state.pi = %v", st.Pi)
	}

	if r := post(t, ts, "/api/pi/test", `{"url":"`+pi+`"}`, nil); r.StatusCode != 200 {
		t.Fatalf("test: %d", r.StatusCode)
	}
	if u, k := o.last(); k != 0 {
		t.Fatalf("Test must not switch anything (opened %q)", u)
	}
	if r := post(t, ts, "/api/pi/connect", `{"url":"`+pi+`/"}`, nil); r.StatusCode != 200 {
		t.Fatalf("connect: %d", r.StatusCode)
	}
	if u, _ := o.last(); u != pi {
		t.Fatalf("window opened %q, want %q", u, pi)
	}
	if s.Poll.Running() != 0 {
		t.Fatal("pollers still running after connecting to the Pi")
	}
	saved, _ := LoadConfig(dir)
	if !saved.PiMode || saved.PiURL != pi {
		t.Fatalf("not saved: %v %q", saved.PiMode, saved.PiURL)
	}
	before := n.Load()
	time.Sleep(2500 * time.Millisecond)
	if after := n.Load(); after != before {
		t.Fatalf("miner read %d more times while showing the Pi", after-before)
	}
	// Restart in Pi mode: still no polling.
	s2 := &Server{Cfg: saved, Poll: NewPoller(t.TempDir(), 2*time.Second), Static: web.FS, Pi: &PiLink{}}
	s2.StartMiners()
	if s2.Poll.Running() != 0 {
		t.Fatal("restart in Pi mode started pollers")
	}

	// Switch back to running on this Mac.
	if r := post(t, ts, "/api/pi/local", `{}`, nil); r.StatusCode != 200 {
		t.Fatalf("local: %d", r.StatusCode)
	}
	if u, _ := o.last(); u != "" {
		t.Fatalf("window opened %q, want this Mac's dashboard", u)
	}
	if s.Poll.Running() != 1 {
		t.Fatal("switching back should read the miner here again")
	}
	saved, _ = LoadConfig(dir)
	if saved.PiMode || saved.PiURL != pi {
		t.Fatalf("after switching back: mode %v url %q (address kept for next time)", saved.PiMode, saved.PiURL)
	}
	s.Poll.Halt(m.ID)
}

func TestPiConnectRefusesNonHashWatch(t *testing.T) {
	var o opened
	cfg, _ := LoadConfig(t.TempDir())
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Pi: o.link()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>router</html>`)) }))
	defer other.Close()
	if r := post(t, ts, "/api/pi/connect", `{"url":"`+other.URL+`"}`, nil); r.StatusCode != 422 {
		t.Fatalf("connect to a non-HashWatch: %d", r.StatusCode)
	}
	if _, k := o.last(); k != 0 || cfg.PiMode {
		t.Fatal("a failed test must not switch modes")
	}
}

// Without the Mac window (Pi service, browser, Windows) the feature doesn't exist.
func TestPiOffWithoutMacWindow(t *testing.T) {
	ts, _ := newTestServer(t, nil, false, "")
	for _, p := range []string{"/api/pi/test", "/api/pi/connect", "/api/pi/local"} {
		if r := post(t, ts, p, `{"url":"http://x"}`, nil); r.StatusCode != 404 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	var st map[string]any
	r, _ := http.Get(ts.URL + "/api/state")
	json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if _, ok := st["pi"]; ok {
		t.Fatal("state.pi present without the Mac window")
	}
}

// With a local password, switching needs a signed-in session (Test doesn't).
func TestPiSwitchNeedsSignIn(t *testing.T) {
	cfg, _ := LoadConfig(t.TempDir())
	cfg.SetPassword("hunter22")
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Pi: &PiLink{}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, p := range []string{"/api/pi/connect", "/api/pi/local"} {
		if r := post(t, ts, p, `{"url":"http://x"}`, nil); r.StatusCode != 401 {
			t.Errorf("%s without sign-in: %d", p, r.StatusCode)
		}
	}
}

// Windows: no app window (PiLink.Open is nil). The browser page navigates to
// the Pi itself, so connect / switch back must work without a window.
func TestPiBrowserModeWithoutWindow(t *testing.T) {
	m, _ := connCounter(t)
	dir := t.TempDir()
	cfg, _ := LoadConfig(dir)
	cfg.Miners = []Miner{m}
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Pi: &PiLink{}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	pi := fakePi(t)
	var st struct {
		Pi map[string]any `json:"pi"`
	}
	r, _ := http.Get(ts.URL + "/api/state")
	json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.Pi == nil || st.Pi["os"] != runtime.GOOS {
		t.Fatalf("state.pi = %v", st.Pi)
	}
	if r := post(t, ts, "/api/pi/connect", `{"url":"`+pi+`"}`, nil); r.StatusCode != 200 {
		t.Fatalf("connect: %d", r.StatusCode)
	}
	if u, on := s.PiActive(); !on || u != pi || s.Poll.Running() != 0 {
		t.Fatalf("pi mode %v %q, pollers %d", on, u, s.Poll.Running())
	}
	if r := post(t, ts, "/api/pi/local", `{}`, nil); r.StatusCode != 200 {
		t.Fatalf("local: %d", r.StatusCode)
	}
	if _, on := s.PiActive(); on || s.Poll.Running() != 1 {
		t.Fatalf("after local: pi %v pollers %d", on, s.Poll.Running())
	}
	s.Poll.Halt(m.ID)
}
