package app

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/web"
)

func fanCount(m *sim.Miner, want string) int {
	n := 0
	for _, l := range m.Log() {
		if l == want {
			n++
		}
	}
	return n
}

func newFanRig(t *testing.T, cooler bool, pct int) (*sim.Miner, *Poller, *FanSync, *Config, string) {
	t.Helper()
	cmdGap = 0
	m := sim.New()
	m.Nano3S = true
	m.SetElapsed(5000)
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	cfg, _ := LoadConfig(t.TempDir())
	cfg.Miners = []Miner{{ID: "s", Name: "Sim 3S", Host: h, Port: port, Kind: "cgminer"}}
	if cooler {
		cfg.Miners[0].Opts.Fan = FanOpts{Mode: "cooler", Percent: pct}
	}
	poll := NewPoller(t.TempDir(), time.Hour)
	poll.Start(cfg.Miners[0])
	t.Cleanup(func() { poll.Stop("s") })
	f := NewFanSync(cfg, poll)
	return m, poll, f, cfg, ln.Addr().String()
}

func TestFanReapplyAfterRestartAndReboot(t *testing.T) {
	m, _, f, _, addr := newFanRig(t, true, 60)
	snap := cgminer.Fetch(addr, 3*time.Second)
	if !snap.OK || !cgminer.SupportsFanSpd(snap) {
		t.Fatalf("sim 3S: ok=%v support=%v err=%v", snap.OK, cgminer.SupportsFanSpd(snap), snap.QueryErrors)
	}
	f.Observe("s", snap)
	if n := fanCount(m, "ascset|0,fan-spd,60"); n != 1 {
		t.Fatalf("HashWatch start should send fan-spd,60 once, got %d log=%v", n, m.Log())
	}

	// A later poll with Elapsed still rising must not send again.
	m.SetElapsed(5100)
	snap = cgminer.Fetch(addr, 3*time.Second)
	f.Observe("s", snap)
	if n := fanCount(m, "ascset|0,fan-spd,60"); n != 1 {
		t.Fatalf("poll-and-override sent again: %d log=%v", n, m.Log())
	}

	// Simulated miner reboot: Elapsed went down.
	m.SetElapsed(30)
	snap = cgminer.Fetch(addr, 3*time.Second)
	if snap.Uptime.Seconds >= 5100 {
		t.Fatalf("elapsed did not drop: %d", snap.Uptime.Seconds)
	}
	f.Observe("s", snap)
	if n := fanCount(m, "ascset|0,fan-spd,60"); n != 2 {
		t.Fatalf("reboot should re-apply fan-spd,60, got %d log=%v", n, m.Log())
	}

	// HashWatch restart: a new FanSync with the same persisted Cooler config.
	_, poll2, f2, _, _ := func() (*sim.Miner, *Poller, *FanSync, *Config, string) {
		// Reuse the same miner: start a new poller + FanSync as a process restart would.
		cmdGap = 0
		cfg, _ := LoadConfig(t.TempDir())
		// copy host/port from first poller's latest
		h, p, _ := net.SplitHostPort(addr)
		port, _ := strconv.Atoi(p)
		cfg.Miners = []Miner{{ID: "s", Name: "Sim 3S", Host: h, Port: port, Kind: "cgminer", Opts: MinerOpts{Fan: FanOpts{Mode: "cooler", Percent: 60}}}}
		poll := NewPoller(t.TempDir(), time.Hour)
		poll.Start(cfg.Miners[0])
		t.Cleanup(func() { poll.Stop("s") })
		return m, poll, NewFanSync(cfg, poll), cfg, addr
	}()
	_ = poll2
	snap = cgminer.Fetch(addr, 3*time.Second)
	f2.Observe("s", snap)
	if n := fanCount(m, "ascset|0,fan-spd,60"); n != 3 {
		t.Fatalf("HashWatch restart should re-apply fan-spd,60, got %d log=%v", n, m.Log())
	}
}

func TestFanAutoSendsMinusOneOnce(t *testing.T) {
	m, poll, f, cfg, addr := newFanRig(t, false, 0)
	cfg.SetPassword("adminpass1")
	s := &Server{Cfg: cfg, Poll: poll, Fan: f, Static: web.FS, InstanceID: NewID()}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	admin := &tclient{t: t, ts: ts}
	if r, _ := admin.do("POST", "/api/login", `{"password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	if r, out := admin.do("POST", "/api/control/fan", `{"miner":"s","mode":"auto","password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatalf("auto: %d %v", r.StatusCode, out)
	}
	if n := fanCount(m, "ascset|0,fan-spd,-1"); n != 1 {
		t.Fatalf("Auto should send fan-spd,-1 once, got %d log=%v", n, m.Log())
	}
	// later polls must not re-send Auto
	for i := 0; i < 3; i++ {
		snap := cgminer.Fetch(addr, 3*time.Second)
		f.Observe("s", snap)
	}
	if n := fanCount(m, "ascset|0,fan-spd,-1"); n != 1 {
		t.Fatalf("Auto re-sent: %d log=%v", n, m.Log())
	}
	got, ok := cfg.minerByID("s")
	if !ok || got.Opts.Fan.Mode != "auto" {
		t.Fatalf("persisted fan: %+v", got.Opts.Fan)
	}
}

func TestFanControlRejectsBadPercent(t *testing.T) {
	m, poll, f, cfg, _ := newFanRig(t, false, 0)
	cfg.SetPassword("adminpass1")
	s := &Server{Cfg: cfg, Poll: poll, Fan: f, Static: web.FS, InstanceID: NewID()}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	admin := &tclient{t: t, ts: ts}
	if r, _ := admin.do("POST", "/api/login", `{"password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	for _, body := range []string{
		`{"miner":"s","mode":"cooler","percent":15,"password":"adminpass1"}`,
		`{"miner":"s","mode":"cooler","percent":39,"password":"adminpass1"}`,
		`{"miner":"s","mode":"cooler","percent":101,"password":"adminpass1"}`,
		`{"miner":"s","mode":"cooler","percent":0,"password":"adminpass1"}`,
		`{"miner":"s","mode":"wind","password":"adminpass1"}`,
	} {
		if r, out := admin.do("POST", "/api/control/fan", body); r.StatusCode != 400 {
			t.Fatalf("expected 400 for %s: %d %v", body, r.StatusCode, out)
		}
	}
	for _, l := range m.Log() {
		if strings.Contains(l, "fan-spd") {
			t.Fatalf("bad request reached the miner: %v", m.Log())
		}
	}
}

func TestFanWorkmodeReappliesCooler(t *testing.T) {
	m, poll, f, cfg, addr := newFanRig(t, true, 50)
	snap := cgminer.Fetch(addr, 3*time.Second)
	f.Observe("s", snap)
	if n := fanCount(m, "ascset|0,fan-spd,50"); n != 1 {
		t.Fatalf("start apply: %d log=%v", n, m.Log())
	}
	cfg.SetPassword("adminpass1")
	s := &Server{Cfg: cfg, Poll: poll, Fan: f, Static: web.FS, InstanceID: NewID()}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	admin := &tclient{t: t, ts: ts}
	if r, _ := admin.do("POST", "/api/login", `{"password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	// Control enforces a 3s gap after the start re-apply Command.
	time.Sleep(3 * time.Second)
	if r, out := admin.do("POST", "/api/control/workmode", `{"miner":"s","mode":"low","password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatalf("workmode: %d %v", r.StatusCode, out)
	}
	if n := fanCount(m, "ascset|0,fan-spd,50"); n != 2 {
		t.Fatalf("workmode should re-apply Cooler 50, got %d log=%v", n, m.Log())
	}
}

func TestFanLiveReportsSupport(t *testing.T) {
	_, poll, f, cfg, addr := newFanRig(t, true, 55)
	// wait until a snapshot is present so /api/live can report support
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if snap := poll.Latest("s"); snap != nil && snap.OK {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = addr
	s := &Server{Cfg: cfg, Poll: poll, Fan: f, Static: web.FS, InstanceID: NewID()}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c := &tclient{t: t, ts: ts}
	_, out := c.do("GET", "/api/live?miner=s", "")
	b, _ := json.Marshal(out["fan"])
	if !strings.Contains(string(b), `"supported":true`) || !strings.Contains(string(b), `"mode":"cooler"`) || !strings.Contains(string(b), `"percent":55`) {
		t.Fatalf("live fan: %s", b)
	}
}

func TestFanHiddenOnNano3(t *testing.T) {
	cmdGap = 0
	m := sim.New() // original Nano 3
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	snap := cgminer.Fetch(ln.Addr().String(), 3*time.Second)
	if !snap.OK {
		t.Fatalf("nano3 sim: %v", snap.QueryErrors)
	}
	if cgminer.SupportsFanSpd(snap) {
		t.Fatal("original Nano 3 must hide Fan control")
	}
}
