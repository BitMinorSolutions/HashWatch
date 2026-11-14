package auto

import (
	"testing"
	"time"
)

var loc = time.UTC

func at(wd time.Weekday, h, m int) time.Time {
	// 2026-11-15 is a Sunday
	return time.Date(2026, 11, 15+int(wd), h, m, 0, 0, loc)
}

func TestScheduleRules(t *testing.T) {
	weekdays := []int{1, 2, 3, 4, 5}
	rules := []Rule{
		{Days: weekdays, Start: "16:00", End: "21:00", Mode: "low"},                    // peak power hours
		{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "22:00", End: "07:00", Mode: "high"}, // overnight
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		t    time.Time
		want string
	}{
		{at(time.Monday, 16, 0), "low"}, {at(time.Monday, 20, 59), "low"}, {at(time.Monday, 21, 0), "medium"},
		{at(time.Saturday, 17, 0), "medium"}, {at(time.Monday, 23, 30), "high"}, {at(time.Tuesday, 6, 59), "high"},
		{at(time.Tuesday, 7, 0), "medium"}, {at(time.Sunday, 2, 0), "high"}, // Saturday-night rule spills into Sunday
	}
	for _, c := range cases {
		if got, _ := Desired(rules, "medium", c.t); got != c.want {
			t.Errorf("%s: got %q want %q", c.t.Format("Mon 15:04"), got, c.want)
		}
	}
	if (Rule{Days: []int{1}, Start: "9:00", End: "9:00", Mode: "low"}).Validate() == nil {
		t.Fatal("same start/end must fail")
	}
	if (Rule{Days: []int{7}, Start: "9:00", End: "10:00", Mode: "low"}).Validate() == nil {
		t.Fatal("bad day must fail")
	}
}

func TestSchedulerOnlyActsOnChange(t *testing.T) {
	rules := []Rule{{Days: []int{1}, Start: "16:00", End: "21:00", Mode: "low"}}
	var s Scheduler
	if m, ok := s.Step(rules, "high", at(time.Monday, 15, 0)); !ok || m != "high" {
		t.Fatal("first step applies the current mode")
	}
	if _, ok := s.Step(rules, "high", at(time.Monday, 15, 30)); ok {
		t.Fatal("no change, no command (manual changes stick)")
	}
	if m, ok := s.Step(rules, "high", at(time.Monday, 16, 0)); !ok || m != "low" {
		t.Fatal("boundary applies low")
	}
	var s2 Scheduler
	if _, ok := s2.Step(rules, "", at(time.Monday, 12, 0)); ok {
		t.Fatal("no default and no rule: leave the miner alone")
	}
}

func TestThrottleHysteresis(t *testing.T) {
	c := ThrottleCfg{HotC: 95, CoolC: 88, Hold: 2 * time.Minute, Cool: 10 * time.Minute, Gap: 3 * time.Minute}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	var th Throttle
	t0 := time.Unix(1794642000, 0)
	lvl := 2
	step := func(dt time.Duration, temp float64) (int, bool) {
		n, act, _ := th.Step(c, t0.Add(dt), temp, lvl)
		if act {
			lvl = n
		}
		return n, act
	}
	if _, act := step(0, 96); act {
		t.Fatal("must wait for Hold")
	}
	if _, act := step(90*time.Second, 96); act {
		t.Fatal("still within Hold")
	}
	if n, act := step(2*time.Minute, 96); !act || n != 1 {
		t.Fatalf("step down to medium: %d %v", n, act)
	}
	// in the hysteresis band (88 < t < 95): hold position, no flapping
	for i := 3; i < 30; i++ {
		if _, act := step(time.Duration(i)*time.Minute, 91); act {
			t.Fatal("no change between cool and hot")
		}
	}
	// still hot again: step down again only after Hold and Gap
	if _, act := step(30*time.Minute, 97); act {
		t.Fatal("hold restarts")
	}
	if n, act := step(32*time.Minute, 97); !act || n != 0 {
		t.Fatalf("second step to low: %d", n)
	}
	if _, act := step(40*time.Minute, 97); act || lvl != 0 {
		t.Fatal("can't go below low")
	}
	// cool: restore one level per Cool period, never above where it started
	if _, act := step(41*time.Minute, 85); act {
		t.Fatal("must stay cool for Cool first")
	}
	if n, act := step(51*time.Minute, 85); !act || n != 1 {
		t.Fatalf("restore to medium: %d", n)
	}
	if n, act := step(61*time.Minute, 85); !act || n != 2 {
		t.Fatalf("restore to high: %d", n)
	}
	if th.Throttled() {
		t.Fatal("fully restored")
	}
	if _, act := step(80*time.Minute, 80); act {
		t.Fatal("never above the original level")
	}
	if (ThrottleCfg{HotC: 90, CoolC: 92}).Validate() == nil {
		t.Fatal("cool must be below hot")
	}
}

func TestThrottleManualOverride(t *testing.T) {
	c := ThrottleCfg{HotC: 95, CoolC: 88, Hold: time.Minute, Cool: time.Minute, Gap: time.Minute}
	var th Throttle
	t0 := time.Unix(0, 0)
	th.Step(c, t0, 96, 2)
	if n, act, _ := th.Step(c, t0.Add(time.Minute), 96, 2); !act || n != 1 {
		t.Fatal("step")
	}
	// user sets High by hand a while later: throttle lets go
	th.Step(c, t0.Add(3*time.Minute), 91, 2)
	if th.Throttled() {
		t.Fatal("manual change should end the throttle")
	}
}

func TestFailover(t *testing.T) {
	c := FailoverCfg{DeadFor: 3 * time.Minute, ReturnToMain: true, ReturnAfter: 10 * time.Minute}
	var f Failover
	t0 := time.Unix(1794642000, 0)
	main := PoolView{ID: 0, Priority: 0, Alive: true, Active: true}
	backup := PoolView{ID: 1, Priority: 1, Alive: true}
	if _, act, _ := f.Step(c, t0, []PoolView{main, backup}); act {
		t.Fatal("healthy: nothing")
	}
	main.Alive = false
	if _, act, _ := f.Step(c, t0.Add(time.Minute), []PoolView{main, backup}); act {
		t.Fatal("must be dead for DeadFor")
	}
	to, act, _ := f.Step(c, t0.Add(4*time.Minute), []PoolView{main, backup})
	if !act || to.ID != 1 {
		t.Fatalf("switch to backup: %+v %v", to, act)
	}
	// now on the backup; main comes back → return after ReturnAfter
	main.Active, backup.Active, main.Alive = false, true, true
	if _, act, _ := f.Step(c, t0.Add(5*time.Minute), []PoolView{main, backup}); act {
		t.Fatal("too soon to return")
	}
	to, act, _ = f.Step(c, t0.Add(16*time.Minute), []PoolView{main, backup})
	if !act || to.ID != 0 {
		t.Fatalf("return to main: %+v %v", to, act)
	}
	// every pool dead: nowhere to go
	var g Failover
	dead := []PoolView{{ID: 0, Active: true}, {ID: 1, Priority: 1}}
	g.Step(c, t0, dead)
	if _, act, _ := g.Step(c, t0.Add(10*time.Minute), dead); act {
		t.Fatal("no alive pool, no switch")
	}
}

func TestRestartLimits(t *testing.T) {
	c := RestartCfg{BelowTHs: 1, For: 5 * time.Minute, Cooldown: 30 * time.Minute, MaxPerDay: 2}
	var r Restarter
	t0 := time.Unix(1794642000, 0)
	if act, _ := r.Step(c, t0, true, 0.2); act {
		t.Fatal("must stall for For")
	}
	if act, _ := r.Step(c, t0.Add(5*time.Minute), true, 0.2); !act {
		t.Fatal("restart after 5 min stalled")
	}
	r.Step(c, t0.Add(6*time.Minute), true, 0.2)
	if act, _ := r.Step(c, t0.Add(12*time.Minute), true, 0.2); act {
		t.Fatal("cooldown")
	}
	if act, _ := r.Step(c, t0.Add(40*time.Minute), true, 0.2); !act {
		t.Fatal("second restart after cooldown")
	}
	r.Step(c, t0.Add(80*time.Minute), true, 0.2)
	if act, _ := r.Step(c, t0.Add(90*time.Minute), true, 0.2); act {
		t.Fatal("max per day")
	}
	if act, _ := r.Step(c, t0.Add(25*time.Hour), true, 0.2); !act {
		t.Fatal("a new 24 h window allows restarts again (still stalled)")
	}
	var r2 Restarter
	r2.Step(c, t0, false, 0)
	if act, _ := r2.Step(c, t0.Add(time.Hour), false, 0); act {
		t.Fatal("offline miner is not a stall (can't be restarted over the API anyway)")
	}
}
