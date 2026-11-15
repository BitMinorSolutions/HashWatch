package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

func deadAddr(t *testing.T) string {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	a := "http://" + ln.Addr().String()
	ln.Close()
	return a
}

func TestTailscalePeers(t *testing.T) {
	st := []byte(`{"Self":{"HostName":"bits-mac","DNSName":"bits-mac.tail1234.ts.net.","TailscaleIPs":["100.64.0.1"],"Online":true},
	"Peer":{
	 "nodekey:a":{"HostName":"iphone","DNSName":"iphone.tail1234.ts.net.","TailscaleIPs":["100.64.0.2"],"Online":true},
	 "nodekey:b":{"HostName":"hashwatch","DNSName":"hashwatch.tail1234.ts.net.","TailscaleIPs":["100.121.124.114","fd7a:115c:a1e0::1"],"Online":true}}}`)
	got := tailscalePeers(st)
	want := []PiCandidate{{"http://hashwatch.tail1234.ts.net:48412", "tailscale"}, {"http://100.121.124.114:48412", "tailscale"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
	if tailscalePeers([]byte("not json")) != nil || len(tailscalePeers([]byte(`{"Peer":{}}`))) != 0 {
		t.Fatal("bad status should give no candidates")
	}
}

func TestDefaultPiCandidatesOrder(t *testing.T) {
	old := tailscaleStatus
	defer func() { tailscaleStatus = old }()
	tailscaleStatus = func(context.Context) []byte { return nil } // no Tailscale CLI
	cs := DefaultPiCandidates(context.Background())
	if len(cs) != 2 || cs[0].URL != "http://hashwatch.local:48412" || cs[0].Via != "lan" || cs[1].URL != "http://hashwatch:48412" || cs[1].Via != "tailscale" {
		t.Fatalf("without Tailscale CLI: %v", cs)
	}
	tailscaleStatus = func(context.Context) []byte {
		return []byte(`{"Peer":{"k":{"HostName":"hashwatch","DNSName":"hashwatch.t.ts.net.","TailscaleIPs":["100.1.2.3"],"Online":true}}}`)
	}
	cs = DefaultPiCandidates(context.Background())
	if len(cs) != 4 || cs[0].Via != "lan" || cs[1].URL != "http://hashwatch.t.ts.net:48412" || cs[2].URL != "http://100.1.2.3:48412" || cs[3].URL != "http://hashwatch:48412" {
		t.Fatalf("with Tailscale: %v", cs)
	}
}

// Only addresses that answer as a HashWatch count; home network first, then Tailscale.
func TestFindPi(t *testing.T) {
	lan, ts := fakePi(t), fakePi(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"app":"Router"}`)) }))
	defer other.Close()
	got := FindPi(context.Background(), []PiCandidate{
		{deadAddr(t), "lan"}, {other.URL, "lan"}, {lan, "lan"},
		{other.URL, "tailscale"}, {ts, "tailscale"}, {lan + "/", "tailscale"},
	})
	if len(got) != 2 || got[0].URL != lan || got[0].Via != "lan" || got[0].Version != "0.7.2" || got[1].URL != ts || got[1].Via != "tailscale" {
		t.Fatalf("found %+v", got)
	}
	// Only Tailscale works (away from home).
	got = FindPi(context.Background(), []PiCandidate{{deadAddr(t), "lan"}, {ts, "tailscale"}})
	if len(got) != 1 || got[0].URL != ts {
		t.Fatalf("away: %+v", got)
	}
	if got := FindPi(context.Background(), []PiCandidate{{other.URL, "lan"}, {deadAddr(t), "tailscale"}}); len(got) != 0 {
		t.Fatalf("nothing should be found: %+v", got)
	}
}

func TestPiFindEndpoint(t *testing.T) {
	pi := fakePi(t)
	cfg, _ := LoadConfig(t.TempDir())
	dead := deadAddr(t)
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Second), Static: web.FS,
		Pi: &PiLink{Candidates: func(context.Context) []PiCandidate {
			return []PiCandidate{{dead, "lan"}, {pi, "tailscale"}}
		}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	code, out := postBody[struct{ Found []PiFound }](t, srv, "/api/pi/find")
	if code != 200 || len(out.Found) != 1 || out.Found[0].URL != pi || out.Found[0].Via != "tailscale" {
		t.Fatalf("find: %d %+v", code, out.Found)
	}
}

// Pi mode: the saved address doesn't answer (left home) -> the other address is used.
func TestResolvePiFallsBackToOtherAddress(t *testing.T) {
	ts := fakePi(t)
	dead := deadAddr(t)
	var o opened
	cfg, _ := LoadConfig(t.TempDir())
	cfg.PiMode, cfg.PiURL = true, dead
	link := o.link()
	link.Candidates = func(context.Context) []PiCandidate {
		return []PiCandidate{{dead, "lan"}, {ts, "tailscale"}}
	}
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Pi: link}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	code, out := postBody[struct{ URL string }](t, srv, "/api/pi/resolve")
	if code != 200 || out.URL != ts {
		t.Fatalf("resolve: %d %q, want %q", code, out.URL, ts)
	}
	if s.PiCurrent() != ts {
		t.Fatalf("current = %q", s.PiCurrent())
	}
	if saved, _ := s.PiActive(); saved != dead {
		t.Fatal("the saved address must not change on its own")
	}
	// state tells the window which address is in use.
	var st struct{ Pi map[string]any }
	rs, _ := http.Get(srv.URL + "/api/state")
	json.NewDecoder(rs.Body).Decode(&st)
	rs.Body.Close()
	if st.Pi["current"] != ts {
		t.Fatalf("state.pi = %v", st.Pi)
	}

	// Saved address works again (back home): it's preferred.
	home := fakePi(t)
	cfg.mu.Lock()
	cfg.PiURL = home
	cfg.mu.Unlock()
	if u := s.ResolvePi(context.Background()); u != home {
		t.Fatalf("back home: %q", u)
	}

	// Nothing answers: a clear error, the offline page stays.
	link.Candidates = func(context.Context) []PiCandidate { return []PiCandidate{{dead, "lan"}} }
	cfg.mu.Lock()
	cfg.PiURL = dead
	cfg.mu.Unlock()
	if r := post(t, srv, "/api/pi/resolve", `{}`, nil); r.StatusCode != 422 {
		t.Fatalf("nothing reachable: %d", r.StatusCode)
	}
	if s.PiCurrent() != "" {
		t.Fatal("current should be cleared")
	}
}

// Pi mode with a miner saved: finding / resolving never touches the miner and starts no pollers.
func TestResolvePiNeverPollsMiner(t *testing.T) {
	m, n := connCounter(t)
	pi := fakePi(t)
	cfg, _ := LoadConfig(t.TempDir())
	cfg.Miners = []Miner{m}
	cfg.PiMode, cfg.PiURL = true, deadAddr(t)
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Second), Static: web.FS,
		Pi: &PiLink{Candidates: func(context.Context) []PiCandidate { return []PiCandidate{{pi, "lan"}} }}}
	s.StartMiners()
	if u := s.ResolvePi(context.Background()); u != pi {
		t.Fatalf("resolve: %q", u)
	}
	time.Sleep(1500 * time.Millisecond)
	if n.Load() != 0 || s.Poll.Running() != 0 {
		t.Fatalf("miner connections %d, pollers %d", n.Load(), s.Poll.Running())
	}
}

func TestPiFindOffWithoutMacWindow(t *testing.T) {
	ts, _ := newTestServer(t, nil, false, "")
	for _, p := range []string{"/api/pi/find", "/api/pi/resolve"} {
		if r := post(t, ts, p, `{}`, nil); r.StatusCode != 404 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
}

// Connecting (or switching back) forgets the address found this session.
func TestPiConnectClearsCurrent(t *testing.T) {
	pi := fakePi(t)
	cfg, _ := LoadConfig(t.TempDir())
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Pi: &PiLink{}}
	s.Pi.cur = "http://elsewhere:48412"
	if _, err := s.PiConnect(pi); err != nil {
		t.Fatal(err)
	}
	if s.PiCurrent() != "" {
		t.Fatal("connect should clear the session address")
	}
}

func postBody[T any](t *testing.T, ts *httptest.Server, path string) (int, T) {
	t.Helper()
	var out T
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	attachCSRF(t, ts, req)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	json.NewDecoder(r.Body).Decode(&out)
	return r.StatusCode, out
}
