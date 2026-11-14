package app

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/web"
)

// HashWatch next to another dashboard (Grumpy Tech's) on the same computer and miner:
// a foreign HTTP server sits on HashWatch's preferred port and a foreign process polls
// the same miner. HashWatch must take another port, answer only as itself, never send
// the miner anything but reads on a fresh install, keep to one connection at a time,
// and the foreign poller must keep getting answers.
func TestCoexistsWithAnotherDashboard(t *testing.T) {
	m := sim.New()
	mln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer mln.Close()
	maddr := mln.Addr().String()
	h, p, _ := net.SplitHostPort(maddr)
	mport, _ := strconv.Atoi(p)

	// HashWatch alone first: never more than one open connection to the miner,
	// even with reads, a control and an automation command all at once.
	cmdGap = 0
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); cgminer.Fetch(maddr, 3*time.Second) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); cgminer.SetWorkMode(maddr, 2) }()
	wg.Wait()
	if c := m.Concurrency(); c != 1 {
		t.Fatalf("HashWatch opened %d miner connections at once; want 1", c)
	}

	// The other dashboard: an HTTP server on HashWatch's preferred port…
	foreign := foreignDashboard(t)
	// …and its own poller hammering the same miner.
	var foreignOK, foreignBad atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if rawSummary(maddr) {
				foreignOK.Add(1)
			} else {
				foreignBad.Add(1)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	defer close(stop)

	cfg, _ := LoadConfig(t.TempDir())
	cfg.Miners = []Miner{{ID: "n3", Name: "Nano 3", Host: h, Port: mport, Kind: "cgminer"}}
	poll := NewPoller(t.TempDir(), 300*time.Millisecond)
	alerts := NewAlerts(t.TempDir())
	stats := NewStats(t.TempDir())
	autos := NewAutomations(cfg, poll, alerts, false)
	poll.OnPoll = func(id, name string, snap *model.Snapshot, _ []model.Sample) {
		alerts.Observe(id, name, snap, nil)
		stats.Observe(id, name, snap)
		autos.Observe(id, name, snap)
	}
	poll.Start(cfg.Miners[0])
	defer poll.Stop("n3")

	ln, port, err := Listen("127.0.0.1", PortCandidates(0, 0, foreign))
	if err != nil {
		t.Fatal(err)
	}
	if port == foreign {
		t.Fatal("took the other dashboard's port")
	}
	s := &Server{Cfg: cfg, Poll: poll, Static: web.FS, InstanceID: NewID(), Port: port, Alerts: alerts, Stats: stats, Auto: autos}
	hs := &http.Server{Handler: s.Handler()}
	go hs.Serve(ln)
	defer hs.Close()

	time.Sleep(2500 * time.Millisecond)
	if body := get(t, LocalURL(port)+"/api/ping"); !strings.Contains(body, s.InstanceID) {
		t.Fatalf("own URL reached something else: %s", body)
	}
	if body := get(t, "http://127.0.0.1:"+strconv.Itoa(foreign)+"/"); !strings.Contains(body, "Avalon Nano 3") {
		t.Fatalf("the other dashboard was disturbed: %s", body)
	}
	if v, _ := poll.Live("n3", 0); v.Latest == nil || !v.Latest.OK || len(v.History) < 3 {
		t.Fatalf("HashWatch couldn't read the shared miner: %+v", v.LastErr)
	}
	if foreignOK.Load() < 10 || foreignBad.Load() > 0 {
		t.Fatalf("other dashboard's polling was disturbed: ok=%d failed=%d", foreignOK.Load(), foreignBad.Load())
	}
	// fresh install: nothing but read commands from HashWatch (the foreign poller only reads too)
	for _, l := range m.Log() {
		cmd := strings.SplitN(l, "|", 2)[0]
		if !cgminer.ReadOnly[cmd] && l != "ascset|0,workmode,set,2" { // the one explicit control sent above
			t.Fatalf("a fresh install changed the miner: %q", l)
		}
	}
}

// rawSummary is the other dashboard's own client (separate connections, no shared locks).
func rawSummary(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(`{"command":"summary"}`))
	b, _ := io.ReadAll(c)
	return strings.Contains(string(b), `"SUMMARY"`)
}
