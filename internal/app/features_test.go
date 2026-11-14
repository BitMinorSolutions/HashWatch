package app

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/auto"
	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/lottery"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
	"github.com/hashwatch/hashwatch/internal/sim"
)

func f64(v float64) *float64 { return &v }

func TestStatsTicketsLeaderboardBadges(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.Local)
	st := NewStats(t.TempDir())
	st.Now = func() time.Time { return now }
	st.Alerts = NewAlerts(t.TempDir())
	net := netdata.Snapshot{Difficulty: 134.8238373232727e12}
	st.Net = func() netdata.Snapshot { return net }
	var events []string
	st.OnEvent = func(id, k string) { events = append(events, k) }
	snap := func(best, blocks float64, up int64) *model.Snapshot {
		return &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs5m: f64(4)}, Shares: model.Shares{BestShare: f64(best), FoundBlocks: f64(blocks)},
			Uptime: model.Uptime{Seconds: up}}
	}
	st.Observe("m", "Nano", snap(5e8, 0, 1000))
	for i := 1; i <= 72; i++ { // six minutes at 4 TH/s in 5 s polls
		now = now.Add(5 * time.Second)
		st.Observe("m", "Nano", snap(5e8, 0, 1000+int64(i*5)))
	}
	v := st.View("m", net)
	if want := 4e12 * 360; v.TicketsDay != want || v.TicketsAll != want {
		t.Fatalf("tickets %v want %v", v.TicketsDay, want)
	}
	if want := lottery.Lambda(4e12, net.Difficulty, 360); !near(v.ExpectedAll, want) {
		t.Fatalf("expected blocks %v want %v", v.ExpectedAll, want)
	}
	if v.TodayAvgTHs != 4 {
		t.Fatalf("avg %v", v.TodayAvgTHs)
	}
	// a gap longer than 90 s (miner off / app closed) isn't counted as hashing
	now = now.Add(10 * time.Minute)
	st.Observe("m", "Nano", snap(5e8, 0, 1700))
	if st.View("m", net).TicketsAll != 4e12*360 {
		t.Fatal("gap counted")
	}
	// new personal best → event + alert; leaderboard sorted
	now = now.Add(5 * time.Second)
	st.Observe("m", "Nano", snap(2.2e9, 0, 1705))
	v = st.View("m", net)
	if len(v.Leaderboard) != 2 || v.Leaderboard[0].Diff != 2.2e9 || v.Leaderboard[1].Diff != 5e8 {
		t.Fatalf("leaderboard %+v", v.Leaderboard)
	}
	if len(events) != 1 || events[0] != "best" {
		t.Fatalf("events %v", events)
	}
	// miner restart: best share resets, a lower session best is still recorded
	now = now.Add(5 * time.Second)
	st.Observe("m", "Nano", snap(3e7, 0, 20))
	if v = st.View("m", net); len(v.Leaderboard) != 3 || v.Leaderboard[2].Diff != 3e7 {
		t.Fatalf("after restart %+v", v.Leaderboard)
	}
	// found block
	now = now.Add(5 * time.Second)
	st.Observe("m", "Nano", snap(3e7, 1, 25))
	v = st.View("m", net)
	if len(v.Blocks) != 1 || events[len(events)-1] != "block" {
		t.Fatalf("block %+v %v", v.Blocks, events)
	}
	earned := map[string]bool{}
	for _, b := range v.Badges {
		if b.Earned != 0 {
			earned[b.ID] = true
		}
	}
	if !earned["first_block"] || !earned["new_pb"] || !earned["share_1g"] || earned["share_1t"] || earned["streak_7"] {
		t.Fatalf("badges %v", earned)
	}
	// daily summary at midnight (no cost field)
	now = time.Date(2026, 11, 15, 0, 0, 30, 0, time.Local)
	st.Observe("m", "Nano", snap(3e7, 1, 30))
	v = st.View("m", net)
	if len(v.Summaries) != 1 || v.Summaries[0].Kind != "daily" || v.Summaries[0].AvgTHs != 4 || v.Summaries[0].BestShare != 2.2e9 {
		t.Fatalf("summary %+v", v.Summaries)
	}
	items, _ := st.Alerts.Since(0, 0)
	kinds := map[string]int{}
	for _, a := range items {
		kinds[a.Kind]++
	}
	if kinds["summary"] != 1 || kinds["nearmiss"] != 1 || kinds["badge"] < 3 {
		t.Fatalf("alerts %v", kinds)
	}
	// persisted
	st.Save()
	st2 := NewStats(st.dir)
	if st2.View("m", net).TicketsAll != v.TicketsAll {
		t.Fatal("not saved")
	}
}

func near(a, b float64) bool { d := a - b; return d < 1e-9*b && d > -1e-9*b }

func TestNearMissThreshold(t *testing.T) {
	now := time.Unix(1794642000, 0)
	st := NewStats(t.TempDir())
	st.Now = func() time.Time { return now }
	st.Alerts = NewAlerts(t.TempDir())
	st.Fun = func() FunSettings { return FunSettings{NearMissDiff: 1e8} }
	obs := func(best float64, up int64) {
		now = now.Add(5 * time.Second)
		st.Observe("m", "Nano", &model.Snapshot{OK: true, Shares: model.Shares{BestShare: f64(best)}, Uptime: model.Uptime{Seconds: up}})
	}
	obs(5e9, 1000) // personal best set (first ever reading: no alert)
	obs(5e9, 1005)
	obs(1e7, 10) // restarted
	obs(2e8, 15) // above threshold but not a PB → near miss
	all, _ := st.Alerts.Since(0, 0)
	var items []Alert
	for _, a := range all {
		if a.Kind == "nearmiss" {
			items = append(items, a)
		}
	}
	if len(items) != 1 || items[0].Title != "Near miss!" || !strings.Contains(items[0].Body, "200.00M") {
		t.Fatalf("%+v", items)
	}
}

func TestStreak(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.Local)
	ms := &MinerStats{}
	for k := 3; k >= 1; k-- {
		ms.Days = append(ms.Days, DayRec{Date: now.AddDate(0, 0, -k).Format("2006-01-02"), OnlineSec: 86000, SeenSec: 86400})
	}
	ms.Days = append(ms.Days, DayRec{Date: now.Format("2006-01-02"), OnlineSec: 40000, SeenSec: 43200})
	if n := ms.Streak(now); n != 4 {
		t.Fatalf("streak %d", n)
	}
	ms.Days[1].OnlineSec = 3600 // a bad day breaks it
	if n := ms.Streak(now); n != 2 {
		t.Fatalf("streak %d", n)
	}
}

// --- automations end to end against the simulator ---

type autoRig struct {
	t    *testing.T
	m    *sim.Miner
	a    *Automations
	cfg  *Config
	addr string
	now  time.Time
}

func newAutoRig(t *testing.T) *autoRig {
	cmdGap = 0
	m := sim.New()
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	cfg, _ := LoadConfig(t.TempDir())
	cfg.Miners = []Miner{{ID: "s", Name: "Sim", Host: h, Port: port, Kind: "cgminer"}}
	poll := NewPoller(t.TempDir(), time.Hour)
	poll.Start(cfg.Miners[0])
	r := &autoRig{t: t, m: m, cfg: cfg, addr: ln.Addr().String(), now: time.Unix(1794642000, 0)}
	r.a = NewAutomations(cfg, poll, NewAlerts(t.TempDir()), false)
	r.a.Sync = true
	r.a.Now = func() time.Time { return r.now }
	return r
}

func (r *autoRig) step(d time.Duration) *model.Snapshot {
	r.now = r.now.Add(d)
	snap := cgminer.Fetch(r.addr, 3*time.Second)
	if !snap.OK {
		r.t.Fatalf("sim fetch: %v", snap.QueryErrors)
	}
	r.a.Observe("s", "Sim", snap)
	return snap
}

func (r *autoRig) opts(f func(*MinerOpts)) { r.cfg.updateMiner("s", func(m *Miner) { f(&m.Opts) }) }

func (r *autoRig) level() int { r.m.Log(); return simLevel(r.m) }

func simLevel(m *sim.Miner) int {
	for i := len(m.Log()) - 1; i >= 0; i-- {
		l := m.Log()[i]
		if strings.HasPrefix(l, "ascset|0,workmode,set,") {
			n, _ := strconv.Atoi(strings.TrimPrefix(l, "ascset|0,workmode,set,"))
			return n
		}
	}
	return 2
}

func (r *autoRig) sent(prefix string) int {
	n := 0
	for _, l := range r.m.Log() {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestAutoThrottleE2E(t *testing.T) {
	r := newAutoRig(t)
	r.opts(func(o *MinerOpts) { o.Throttle = ThrottleOpts{On: true, HotC: 95, CoolC: 90, HoldMin: 1, CoolMin: 2} })
	r.step(0) // sim at High: TMax 92 → nothing
	if r.sent("ascset|0,workmode") != 0 {
		t.Fatal("not hot yet")
	}
	r.m.ExtraHeat = 6 // 98 °C
	r.step(5 * time.Second)
	r.step(61 * time.Second)
	if r.level() != 1 || r.sent("ascset|0,workmode") != 1 {
		t.Fatalf("should step down to medium: %v", r.m.Log())
	}
	r.m.ExtraHeat = -4 // medium: 86+3-4 = 85 °C ≤ 90
	r.step(4 * time.Minute)
	r.step(2*time.Minute + time.Second)
	if r.level() != 2 {
		t.Fatalf("should restore high: %v", r.m.Log())
	}
	v := r.a.View("s")
	if acts := v["actions"].([]Action); len(acts) != 2 || !acts[0].OK || !strings.Contains(acts[1].Text, "auto-throttle") {
		t.Fatalf("actions %+v", acts)
	}
}

func TestScheduleE2E(t *testing.T) {
	r := newAutoRig(t)
	wd := int(r.now.Weekday())
	hh := r.now.Format("15:04")
	end := r.now.Add(time.Hour).Format("15:04")
	r.opts(func(o *MinerOpts) {
		o.Schedule = ScheduleOpts{On: true, Default: "high", Rules: []auto.Rule{{Days: []int{wd}, Start: hh, End: end, Mode: "low"}}}
	})
	r.step(time.Second)
	if r.level() != 0 {
		t.Fatalf("rule says low: %v", r.m.Log())
	}
	r.step(time.Minute)
	if r.sent("ascset|0,workmode") != 1 {
		t.Fatal("no repeat commands")
	}
	r.step(time.Hour)
	if r.level() != 2 {
		t.Fatal("after the rule: default high")
	}
}

func TestFailoverE2E(t *testing.T) {
	r := newAutoRig(t)
	r.opts(func(o *MinerOpts) { o.Failover = FailoverOpts{On: true, DeadMin: 2, ReturnToMain: true, ReturnMin: 5} })
	r.step(0)
	r.m.PoolDown = true
	r.step(5 * time.Second)
	r.step(time.Minute)
	if r.sent("switchpool") != 0 {
		t.Fatal("too early")
	}
	r.step(2 * time.Minute)
	if r.m.Active != 1 || r.sent("switchpool|1") != 1 {
		t.Fatalf("should switch to backup pool 1: %v active=%d", r.m.Log(), r.m.Active)
	}
	r.m.PoolDown = false
	r.step(time.Minute)
	r.step(6 * time.Minute)
	if r.m.Active != 0 {
		t.Fatalf("should return to main: %v", r.m.Log())
	}
}

func TestRestartE2E(t *testing.T) {
	r := newAutoRig(t)
	r.opts(func(o *MinerOpts) {
		o.Restart = RestartOpts{On: true, BelowTHs: 1, StallMin: 5, CooldownMin: 30, MaxPerDay: 2}
	})
	r.m.HashFactor = 0.02
	r.step(0)
	r.step(5*time.Minute + time.Second)
	if r.m.Restarts != 1 || r.sent("restart") != 1 {
		t.Fatalf("restart expected: %v", r.m.Log())
	}
	time.Sleep(4200 * time.Millisecond) // the sim is "restarting"
	r.step(6 * time.Minute)
	if r.m.Restarts != 1 {
		t.Fatal("cooldown")
	}
}

func TestLightEffectsE2E(t *testing.T) {
	r := newAutoRig(t)
	r.opts(func(o *MinerOpts) {
		o.Light = LightOpts{Lamp: true, Celebrate: true, BestFlash: true, Base: &LightState{Mode: "pulse", Color: "#ff6a00", Bright: 82}}
	})
	r.step(0)
	if r.m.LED != [6]int{1, 60, 50, 48, 209, 88} {
		t.Fatalf("healthy lamp = steady green: %v", r.m.LED)
	}
	r.m.PoolDown = true
	r.step(10 * time.Second)
	if r.m.LED[0] != 2 || r.m.LED[3] != 255 || r.m.LED[4] != 59 {
		t.Fatalf("problem lamp = flashing red: %v", r.m.LED)
	}
	r.m.PoolDown = false
	r.a.Event("s", "block")
	r.step(10 * time.Second)
	if r.m.LED != [6]int{2, 100, 50, 255, 196, 0} {
		t.Fatalf("block = flashing gold: %v", r.m.LED)
	}
	r.step(61 * time.Second)
	if r.m.LED[0] != 1 || r.m.LED[4] != 209 {
		t.Fatalf("back to the lamp: %v", r.m.LED)
	}
	// lamp off → the user's own light comes back
	r.opts(func(o *MinerOpts) { o.Light.Lamp = false })
	r.step(10 * time.Second)
	if r.m.LED != [6]int{3, 82, 50, 255, 106, 0} {
		t.Fatalf("restore user's light: %v", r.m.LED)
	}
	n := len(r.m.Log())
	r.step(10 * time.Second)
	if len(r.m.Log()) != n+5 { // only the 5 read commands of a poll
		t.Fatalf("no extra light commands once restored: %v", r.m.Log()[n:])
	}
	// best-share flash is rate-limited
	r.a.Event("s", "best")
	r.step(10 * time.Second)
	if r.m.LED[3] != 64 {
		t.Fatalf("best flash cyan: %v", r.m.LED)
	}
	r.step(13 * time.Second)
	r.a.Event("s", "best")
	r.step(10 * time.Second)
	if r.m.LED[3] == 64 {
		t.Fatal("second best flash within 10 min must be skipped")
	}
}

func TestAutomationsOffByDefaultAndLocked(t *testing.T) {
	r := newAutoRig(t)
	r.m.ExtraHeat = 20
	r.m.PoolDown = true
	r.m.HashFactor = 0.01
	for i := 0; i < 5; i++ {
		r.step(10 * time.Minute)
	}
	for _, l := range r.m.Log() {
		if !cgminer.ReadOnly[strings.SplitN(l, "|", 2)[0]] {
			t.Fatalf("fresh install sent %q", l)
		}
	}
	r.opts(func(o *MinerOpts) { o.Throttle.On = true })
	r.a.Locked = true
	r.step(10 * time.Minute)
	r.step(10 * time.Minute)
	if r.sent("ascset") != 0 {
		t.Fatal("preview never automates")
	}
}
