package app

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/sim"
)

func fp(v float64) *float64 { return &v }

func kinds(a *Alerts) []string {
	items, _ := a.Since(0, 0)
	var k []string
	for _, it := range items {
		k = append(k, it.Kind+":"+it.Level)
	}
	return k
}

func TestAlertRules(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	a := NewAlerts(t.TempDir())
	a.Now = func() time.Time { return now }
	snap := func(h5m, tmax float64, blocks float64, pool bool) *model.Snapshot {
		s := &model.Snapshot{OK: true}
		s.Hashrate.THs5m, s.Temps.Avg, s.Temps.Max, s.Temps.Target, s.Shares.FoundBlocks = fp(h5m), fp(tmax), fp(tmax), fp(90), fp(blocks)
		s.Pools = []model.Pool{{Status: "Alive", StratumActive: pool}}
		return s
	}
	var hist []model.Sample
	step := func(s *model.Snapshot) {
		now = now.Add(15 * time.Second)
		if s.OK {
			hist = append(hist, model.Sample{T: now.Unix(), H5m: s.Hashrate.THs5m})
		}
		a.Observe("m1", "Nano", s, hist)
	}
	for i := 0; i < 240; i++ { // an hour of normal running
		step(snap(4, 85, 0, true))
	}
	if k := kinds(a); len(k) != 0 {
		t.Fatalf("unexpected alerts %v", k)
	}
	step(snap(4, 85, 1, true)) // block!
	for i := 0; i < 3; i++ {
		step(&model.Snapshot{OK: false})
	}
	step(snap(4, 85, 1, true))
	for i := 0; i < 3; i++ {
		step(snap(4, 85, 1, false))
	}
	step(snap(4, 85, 1, true))
	for i := 0; i < 21; i++ { // >5 min hot
		step(snap(4, 93, 1, true))
	}
	for i := 0; i < 42; i++ { // >10 min at half hashrate
		step(snap(2, 85, 1, true))
	}
	want := []string{"block:celebrate", "offline:bad", "offline:good", "pool:bad", "pool:good", "temp:warn", "hashrate:warn"}
	got := kinds(a)
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// a disabled kind is not recorded
	a.Enabled = func(k string) bool { return k != "block" }
	step(snap(2, 85, 2, true))
	if len(kinds(a)) != len(want) {
		t.Fatal("disabled block alert fired")
	}
}

// End to end against the simulator: bump Found Blocks, the poller sees it, an alert fires.
func TestBlockAlertWithSimulator(t *testing.T) {
	m := sim.New()
	ln, _ := m.Listen("127.0.0.1:0")
	defer ln.Close()
	h, ps, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(ps)
	dir := t.TempDir()
	a := NewAlerts(dir)
	var notified []Alert
	a.Notify = func(al Alert) { notified = append(notified, al) }
	p := NewPoller(dir, 100*time.Millisecond)
	p.OnPoll = func(id, name string, s *model.Snapshot, hist []model.Sample) { a.Observe(id, name, s, hist) }
	p.Start(Miner{ID: "s", Name: "Sim", Host: h, Port: port, Kind: "cgminer"})
	defer p.Stop("s")
	time.Sleep(400 * time.Millisecond)
	m.Blocks++
	time.Sleep(600 * time.Millisecond)
	items, _ := a.Since(0, 0)
	if len(items) != 1 || items[0].Kind != "block" {
		t.Fatalf("want one block alert, got %+v", items)
	}
	for _, c := range m.Log() {
		if len(c) >= 6 && c[:6] == "ascset" {
			t.Fatal("alerts must never write to the miner")
		}
	}
}
