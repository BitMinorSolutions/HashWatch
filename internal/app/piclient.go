package app

// "Connect to my Pi": the Mac app can show a HashWatch that runs somewhere else
// (a Raspberry Pi running 24/7) instead of reading the miner itself. In that
// mode this copy starts no miner pollers at all, so the miner is only read once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
)

// PiLink is set by the macOS app window and by HashWatch.exe on Windows (where
// the browser tab navigates to the Pi itself). Nil means the feature is off
// (Pi service, Linux): the endpoints answer 404 and nothing changes.
type PiLink struct {
	// Open shows url in the app window; "" means this Mac's own dashboard.
	// Nil on Windows: the dashboard page in the browser navigates by itself.
	Open func(url string)
	// Candidates replaces the addresses tried when finding the Pi (tests, previews).
	Candidates func(ctx context.Context) []PiCandidate

	mu  sync.Mutex
	cur string // address in use this session when it isn't the saved one
}

// PiInfo is what the Test button found at the address.
type PiInfo struct {
	URL     string `json:"url"`
	App     string `json:"app"`
	Version string `json:"version"`
}

// NormalizePiURL turns "hashwatch.local:48412" or "http://1.2.3.4:48412/x" into
// "http://host:port" (scheme + host only).
func NormalizePiURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Enter your Pi's address, like http://hashwatch.local:48412.")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("That doesn't look like an address. Try http://hashwatch.local:48412.")
	}
	return u.Scheme + "://" + u.Host, nil
}

var piClient = &http.Client{Timeout: 6 * time.Second}

// TestPi checks that the address answers like a HashWatch (GET /api/state, app name).
func TestPi(raw string) (PiInfo, error) {
	return testPi(context.Background(), raw)
}

func testPi(ctx context.Context, raw string) (PiInfo, error) {
	base, err := NormalizePiURL(raw)
	if err != nil {
		return PiInfo{}, err
	}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/state", nil)
	if err != nil {
		return PiInfo{}, err
	}
	resp, err := piClient.Do(req)
	if err != nil {
		return PiInfo{}, fmt.Errorf("Couldn't reach %s. Check that the Pi is on and on the same network (or that Tailscale is on).", host)
	}
	defer resp.Body.Close()
	var st struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st) != nil || st.App != AppName {
		return PiInfo{}, fmt.Errorf("Something answered at %s, but it isn't %s. Check the port (the Pi uses 48412).", host, AppName)
	}
	return PiInfo{URL: base, App: st.App, Version: st.Version}, nil
}

// PiActive reports whether this Mac is showing a Pi instead of reading miners.
func (s *Server) PiActive() (string, bool) {
	if s.Pi == nil {
		return "", false
	}
	s.Cfg.mu.Lock()
	defer s.Cfg.mu.Unlock()
	return s.Cfg.PiURL, s.Cfg.PiMode && s.Cfg.PiURL != ""
}

// StartMiners starts reading every saved miner, unless this Mac is showing a Pi.
func (s *Server) StartMiners() {
	if _, on := s.PiActive(); on {
		return
	}
	for _, m := range s.Cfg.Miners {
		s.Poll.Start(m)
	}
}

func (s *Server) startMiner(m Miner) {
	if _, on := s.PiActive(); on {
		return
	}
	s.Poll.Start(m)
}

// PiConnect tests the address, saves it, stops this Mac's own miner reads and
// shows the Pi in the window.
func (s *Server) PiConnect(raw string) (PiInfo, error) {
	if s.Pi == nil {
		return PiInfo{}, errors.New("not available")
	}
	info, err := TestPi(raw)
	if err != nil {
		return info, err
	}
	s.Cfg.mu.Lock()
	s.Cfg.PiMode, s.Cfg.PiURL = true, info.URL
	s.Pi.mu.Lock()
	s.Pi.cur = ""
	s.Pi.mu.Unlock()
	ms := append([]Miner(nil), s.Cfg.Miners...)
	s.Cfg.mu.Unlock()
	if err := s.Cfg.Save(); err != nil {
		return info, err
	}
	for _, m := range ms {
		s.Poll.Halt(m.ID)
	}
	if s.Pi.Open != nil {
		s.Pi.Open(info.URL)
	}
	return info, nil
}

// PiLocal switches back to running here: miners are read by this Mac again.
func (s *Server) PiLocal() error {
	if s.Pi == nil {
		return errors.New("not available")
	}
	s.Cfg.mu.Lock()
	s.Cfg.PiMode = false // the address is kept to prefill "Connect to my Pi"
	s.Cfg.mu.Unlock()
	s.Pi.mu.Lock()
	s.Pi.cur = ""
	s.Pi.mu.Unlock()
	if err := s.Cfg.Save(); err != nil {
		return err
	}
	s.StartMiners()
	if s.Pi.Open != nil {
		s.Pi.Open("")
	}
	return nil
}

// piBrief is added to /api/state for this Mac's own window only.
func (s *Server) piBrief(r *http.Request) map[string]any {
	if s.Pi == nil || !IsLoopback(r) {
		return nil
	}
	u, on := s.PiActive()
	if u == "" {
		s.Cfg.mu.Lock()
		u = s.Cfg.PiURL
		s.Cfg.mu.Unlock()
	}
	return map[string]any{"mode": on, "url": u, "current": s.PiCurrent(), "os": runtime.GOOS}
}

// piOnly: the Mac's own window only (loopback, same-origin JSON).
func (s *Server) piOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Pi == nil || !IsLoopback(r) {
			writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		h(w, r)
	}
}

func piBody(w http.ResponseWriter, r *http.Request) string {
	var b struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b)
	return b.URL
}

func (s *Server) piTest(w http.ResponseWriter, r *http.Request) {
	info, err := TestPi(piBody(w, r))
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "url": info.URL, "app": info.App, "version": info.Version})
}

func (s *Server) piConnect(w http.ResponseWriter, r *http.Request) {
	info, err := s.PiConnect(piBody(w, r))
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "url": info.URL})
}

func (s *Server) piLocal(w http.ResponseWriter, r *http.Request) {
	if err := s.PiLocal(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
