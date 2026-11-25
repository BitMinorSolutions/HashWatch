package app

import (
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/mining"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/internal/source"
)

func bigOf(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad hashes %q", s)
	}
	return n
}

// Round counter end to end against the simulator: exact count from Total MH,
// a simulated miner restart, the event log entry and the alert switch.
func TestRoundCounterWithSimRestart(t *testing.T) {
	m := sim.New()
	m.Nano3S = true
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dir := t.TempDir()
	p := NewPoller(dir, time.Second)
	tip := int64(975730) // the sim's pool works on 975731
	p.Tip = func(*model.Snapshot) int64 { return tip }
	alerts := NewAlerts(dir)
	alertsOn := false
	alerts.Enabled = func(string) bool { return alertsOn }
	var notified atomic.Int32
	alerts.Notify = func(Alert) { notified.Add(1) }
	p.OnRestart = func(id, name string, r mining.Restart) { alerts.MinerRestarted(id, name, r.DownSec, r.Unknown) }
	st := &minerState{src: source.CGMiner{Addr: ln.Addr().String()}, name: "Sim 3S", stop: make(chan struct{})}
	p.m["s"] = st
	poll := func() { p.pollOnce("s", st) }

	poll() // HashWatch just started: this block arrived before it was watching
	if v := p.RoundView("s"); v == nil || v.Exact || !v.Partial {
		t.Fatalf("first round should be partial: %+v", v)
	}
	m.SetBlockHeight(975732) // the pool hears of the next block first
	poll()                   // new block seen live
	v := p.RoundView("s")
	if v == nil || !v.Exact || v.Height != 975731 {
		t.Fatalf("live round: %+v", v)
	}
	time.Sleep(1200 * time.Millisecond)
	poll()
	v = p.RoundView("s")
	got := bigOf(t, v.Hashes)
	// ~1.2 s at the sim's 3.95 TH/s (work level 2).
	lo, hi := big.NewInt(3e12), big.NewInt(8e12)
	if got.Cmp(lo) < 0 || got.Cmp(hi) > 0 {
		t.Fatalf("count after ~1.2 s = %s, want 3e12..8e12", v.Hashes)
	}
	before := got

	// Simulated restart with 2 s of downtime.
	m.Restart(2 * time.Second)
	poll()
	if len(alerts.items) != 0 {
		t.Fatal("a failed poll alone must not log a restart")
	}
	time.Sleep(3500 * time.Millisecond)
	poll()
	v = p.RoundView("s")
	if !v.Exact || v.NRestarts != 1 || len(v.Restarts) != 1 || v.Restarts[0].DownSec < 1 {
		t.Fatalf("round after restart: %+v", v)
	}
	if bigOf(t, v.Hashes).Cmp(before) < 0 {
		t.Fatalf("restart lost the pre-reboot count: %s < %s", v.Hashes, before)
	}
	items, _ := alerts.Since(0, 0)
	if len(items) != 1 || items[0].Kind != "offline" || !strings.Contains(items[0].Title, "restarted") || items[0].T == 0 {
		t.Fatalf("restart event not logged: %+v", items)
	}
	time.Sleep(50 * time.Millisecond)
	if notified.Load() != 0 {
		t.Fatal("desktop alert fired with alerts turned off")
	}

	// Same again with alerts on: the desktop alert fires too.
	alertsOn = true
	time.Sleep(1200 * time.Millisecond)
	poll()
	m.Restart(2 * time.Second)
	poll()
	time.Sleep(3500 * time.Millisecond)
	poll()
	time.Sleep(50 * time.Millisecond)
	if notified.Load() != 1 {
		t.Fatalf("desktop alert should fire once with alerts on, got %d", notified.Load())
	}
	if v = p.RoundView("s"); v.NRestarts != 2 {
		t.Fatalf("restarts %d", v.NRestarts)
	}

	// Read-only: the round counter only ever used the normal status reads.
	for _, l := range m.Log() {
		c := strings.SplitN(l, "|", 2)[0]
		switch c {
		case "summary", "version", "pools", "devs", "estats", "stats":
		default:
			t.Fatalf("sent a non-read command to the miner: %q", l)
		}
	}
}
