package mining

import (
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

// rsnap is a good poll: uptime elapsed s, Total MH mh.
func rsnap(elapsed int64, mh float64) *model.Snapshot {
	return &model.Snapshot{OK: true, Uptime: model.Uptime{Seconds: elapsed}, Shares: model.Shares{TotalMH: &mh}}
}

const rate = 5e6 // MH/s (5 TH/s)

type rig struct {
	t       *testing.T
	r       *Rounds
	now     time.Time
	elapsed int64
	mh      float64
}

func newRig(t *testing.T, dir string) *rig {
	// Real data shape: Total MH 1024720340040.0 at Elapsed 204980.
	return &rig{t: t, r: NewRounds(dir), now: time.Unix(1795300000, 0), elapsed: 204980, mh: 1024720340040.0}
}

// step advances the miner and the clock by sec seconds and polls once.
func (g *rig) step(sec int64, tip int64) *Restart {
	g.now = g.now.Add(time.Duration(sec) * time.Second)
	g.elapsed += sec
	g.mh += rate * float64(sec)
	return g.r.Observe("m", rsnap(g.elapsed, g.mh), tip, g.now)
}

func (g *rig) view() *RoundView {
	v := g.r.View("m")
	if v == nil {
		g.t.Fatal("no round view")
	}
	return v
}

func hashes(sec int64) string { return itoa(int64(rate*1e6) * sec) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestRoundFirstSeenIsPartial(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.step(5, 900000)
	v := g.view()
	if v.Exact || !v.Partial || v.PartialWhy != PartialOffline || v.Height != 900000 {
		t.Fatalf("first block HashWatch didn't see arrive must be partial: %+v", v)
	}
}

func TestRoundNormalAndNewBlock(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.step(5, 900000) // partial round (HashWatch just started)
	g.step(5, 900000)
	g.step(5, 900001) // new block seen live: snapshot here
	v := g.view()
	if !v.Exact || v.Partial || v.Height != 900001 || v.Hashes != "0" {
		t.Fatalf("new round: %+v", v)
	}
	for i := 0; i < 120; i++ { // 10 minutes of 5 s polls
		g.step(5, 900001)
	}
	v = g.view()
	if !v.Exact || v.Hashes != hashes(600) {
		t.Fatalf("after 600 s want %s hashes, got %+v", hashes(600), v)
	}
	if v.AtMs != g.now.UnixMilli() {
		t.Fatalf("anchor time %d want %d", v.AtMs, g.now.UnixMilli())
	}
	g.step(5, 900002)
	if v = g.view(); v.Height != 900002 || v.Hashes != "0" || !v.Exact {
		t.Fatalf("second round: %+v", v)
	}
	// An older tip (a lagging source) never restarts the round.
	g.step(5, 900001)
	if v = g.view(); v.Height != 900002 || v.Hashes != hashes(5) {
		t.Fatalf("older tip changed the round: %+v", v)
	}
}

func TestRoundRebootMidRound(t *testing.T) {
	g := newRig(t, t.TempDir())
	g.step(5, 900000)
	g.step(5, 900001) // live round
	for i := 0; i < 60; i++ {
		g.step(5, 900001) // 300 s counted
	}
	// Miner reboots: polls fail for 40 s, then it is back with 12 s of uptime.
	for i := 0; i < 3; i++ {
		g.now = g.now.Add(15 * time.Second)
		if rs := g.r.Observe("m", &model.Snapshot{OK: false}, 900001, g.now); rs != nil {
			t.Fatal("failed poll reported a restart")
		}
	}
	g.now = g.now.Add(7 * time.Second) // 52 s since the last good read
	g.elapsed, g.mh = 12, rate*12
	rs := g.r.Observe("m", rsnap(g.elapsed, g.mh), 900001, g.now)
	if rs == nil {
		t.Fatal("restart not detected")
	}
	if rs.DownSec != 40 || rs.Unknown || rs.T != g.now.Unix()-12 {
		t.Fatalf("restart %+v", rs)
	}
	v := g.view()
	if !v.Exact || v.Partial || v.NRestarts != 1 || len(v.Restarts) != 1 {
		t.Fatalf("round after restart: %+v", v)
	}
	if v.Hashes != hashes(300+12) {
		t.Fatalf("count must keep the pre-reboot %s and add the new total: want %s got %s", hashes(300), hashes(312), v.Hashes)
	}
	g.step(5, 900001)
	if v = g.view(); v.Hashes != hashes(317) {
		t.Fatalf("after restart keeps counting: %s", v.Hashes)
	}
	// Total MH going down alone (uptime not reported) is a restart too.
	g2 := newRig(t, "")
	g2.step(5, 900000)
	g2.step(5, 900001)
	g2.step(5, 900001)
	g2.now = g2.now.Add(5 * time.Second)
	if rs := g2.r.Observe("m", rsnap(0, rate*3), 900001, g2.now); rs == nil {
		t.Fatal("Total MH drop not treated as restart")
	}
	if v := g2.r.View("m"); v.Hashes != hashes(5+3) {
		t.Fatalf("Total MH drop count %s", v.Hashes)
	}
	// The next round starts clean.
	g.step(5, 900002)
	if v = g.view(); v.NRestarts != 0 || len(v.Restarts) != 0 || v.Hashes != "0" {
		t.Fatalf("restart notes carried into the next round: %+v", v)
	}
}

func TestRoundHashWatchOfflineGap(t *testing.T) {
	// A reboot hidden by a gap: HashWatch stopped polling for 20 minutes and the
	// miner rebooted, coming back with more uptime than at the last read.
	g := newRig(t, t.TempDir())
	g.elapsed, g.mh = 100, rate*100
	g.step(5, 900000)
	g.step(5, 900001) // live round
	g.step(5, 900001)
	g.now = g.now.Add(20 * time.Minute)
	g.elapsed, g.mh = 600, rate*600 // rebooted 10 minutes ago
	rs := g.r.Observe("m", rsnap(g.elapsed, g.mh), 900001, g.now)
	if rs == nil || !rs.Unknown {
		t.Fatalf("hidden restart not detected as unknown: %+v", rs)
	}
	v := g.view()
	if v.Exact || !v.Partial || v.PartialWhy != PartialOffline {
		t.Fatalf("round with a restart during a HashWatch gap must be partial: %+v", v)
	}

	// A block that lands while HashWatch is offline: partial.
	g = newRig(t, t.TempDir())
	g.step(5, 900000)
	g.step(5, 900001)
	g.now = g.now.Add(5 * time.Minute)
	g.step(5, 900002)
	if v := g.view(); v.Exact || !v.Partial || v.PartialWhy != PartialOffline {
		t.Fatalf("block during gap: %+v", v)
	}
	// A gap with no restart and no new block keeps the round exact (the counter is continuous).
	g = newRig(t, t.TempDir())
	g.step(5, 900000)
	g.step(5, 900001)
	g.step(605, 900001) // one poll 10 minutes after the last, the miner kept its uptime
	if v := g.view(); !v.Exact || v.Hashes != hashes(605) {
		t.Fatalf("gap without restart: %+v", v)
	}
	// A block that lands while the miner isn't answering: partial (unreachable).
	g = newRig(t, t.TempDir())
	g.step(5, 900000)
	g.step(5, 900001)
	g.now = g.now.Add(15 * time.Second)
	g.r.Observe("m", &model.Snapshot{OK: false}, 900002, g.now)
	g.step(15, 900002)
	if v := g.view(); v.Exact || v.PartialWhy != PartialUnreachable || v.Height != 900002 {
		t.Fatalf("block while unreachable: %+v", v)
	}
}

func TestRoundPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	g := newRig(t, dir)
	g.step(5, 900000)
	g.step(5, 900001) // live round, saved at the snapshot
	for i := 0; i < 12; i++ {
		g.step(5, 900001)
	}
	g.r.Save()
	// HashWatch restarts 20 s later; the miner kept hashing.
	g.r = NewRounds(dir)
	g.step(20, 900001)
	v := g.view()
	if !v.Exact || v.Height != 900001 || v.Hashes != hashes(80) {
		t.Fatalf("after HashWatch restart: %+v want %s", v, hashes(80))
	}
	// The miner rebooted while HashWatch was down for 30 minutes: partial, restart logged.
	g.r.Save()
	g.r = NewRounds(dir)
	g.now = g.now.Add(30 * time.Minute)
	g.elapsed, g.mh = 60, rate*60
	rs := g.r.Observe("m", rsnap(g.elapsed, g.mh), 900001, g.now)
	if rs == nil || !rs.Unknown {
		t.Fatalf("restart during HashWatch downtime: %+v", rs)
	}
	if v := g.view(); v.Exact || !v.Partial {
		t.Fatalf("want partial: %+v", v)
	}
	// A new block seen right after a HashWatch restart is partial; the next one is exact.
	g.r.Save()
	g.r = NewRounds(dir)
	g.step(5, 900002)
	if v := g.view(); v.Exact {
		t.Fatalf("block seen on first poll after restart must be partial: %+v", v)
	}
	g.step(5, 900002)
	g.step(5, 900003)
	if v := g.view(); !v.Exact || v.Hashes != "0" {
		t.Fatalf("next round exact: %+v", v)
	}
}

func TestRoundNoCounter(t *testing.T) {
	r := NewRounds("")
	now := time.Unix(1795300000, 0)
	r.Observe("m", &model.Snapshot{OK: true, Uptime: model.Uptime{Seconds: 50}}, 900000, now)
	v := r.View("m")
	if v == nil || v.Counter || v.Exact {
		t.Fatalf("no Total MH: %+v", v)
	}
}

func TestRoundLongRoundExactDigits(t *testing.T) {
	g := newRig(t, "")
	g.step(5, 900000)
	g.step(5, 900001)
	for i := 0; i < 720; i++ { // one hour: > 2^53 hashes
		g.step(5, 900001)
	}
	if v := g.view(); v.Hashes != hashes(3600) {
		t.Fatalf("hour round %s want %s", v.Hashes, hashes(3600))
	}
}
