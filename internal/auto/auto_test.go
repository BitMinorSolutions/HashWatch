package auto

import (
	"strings"
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

func TestFailover(t *testing.T) {
	c := FailoverCfg{DeadFor: 5 * time.Minute, Cooldown: 5 * time.Minute, ReturnAfter: 15 * time.Minute}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	var f Failover
	t0 := time.Unix(1794642000, 0)
	main := PoolView{ID: 0, Priority: 0, Alive: true, Active: true}
	backup := PoolView{ID: 1, Priority: 1, Alive: true}
	if _, act, _ := f.Step(c, t0, []PoolView{main, backup}); act {
		t.Fatal("healthy: nothing")
	}
	main.Alive = false
	if _, act, _ := f.Step(c, t0.Add(time.Minute), []PoolView{main, backup}); act {
		t.Fatal("must be dead for DeadFor (default 5 min)")
	}
	if _, act, _ := f.Step(c, t0.Add(5*time.Minute), []PoolView{main, backup}); act {
		t.Fatal("still within DeadFor")
	}
	to, act, why := f.Step(c, t0.Add(6*time.Minute), []PoolView{main, backup})
	if !act || to.ID != 1 || !strings.Contains(why, "unreachable") {
		t.Fatalf("switch to backup: %+v %v %q", to, act, why)
	}
	// now on the backup; main comes back. Cooldown, then ReturnAfter from when main looked healthy.
	main.Active, backup.Active, main.Alive = false, true, true
	if _, act, _ := f.Step(c, t0.Add(8*time.Minute), []PoolView{main, backup}); act {
		t.Fatal("cooldown blocks return")
	}
	if _, act, _ := f.Step(c, t0.Add(16*time.Minute), []PoolView{main, backup}); act {
		t.Fatal("too soon to return (ReturnAfter is 15 min of a healthy main)")
	}
	to, act, why = f.Step(c, t0.Add(23*time.Minute), []PoolView{main, backup})
	if !act || to.ID != 0 || !strings.Contains(why, "healthy again") {
		t.Fatalf("return to main: %+v %v %q", to, act, why)
	}
	// rejecting counts like dead
	var r Failover
	rej, ok := main, backup
	rej.Active, rej.Alive, rej.Rejecting, ok.Active = true, true, true, false
	r.Step(c, t0, []PoolView{rej, ok})
	to, act, why = r.Step(c, t0.Add(5*time.Minute), []PoolView{rej, ok})
	if !act || to.ID != 1 || !strings.Contains(why, "rejecting") {
		t.Fatalf("rejecting should switch: %+v %v %q", to, act, why)
	}
	// every pool dead: nowhere to go
	var g Failover
	dead := []PoolView{{ID: 0, Active: true}, {ID: 1, Priority: 1}}
	g.Step(c, t0, dead)
	if _, act, _ := g.Step(c, t0.Add(10*time.Minute), dead); act {
		t.Fatal("no alive pool, no switch")
	}
	// a single pool: never switch (can't, and must not invent one)
	var one Failover
	if _, act, _ := one.Step(c, t0.Add(10*time.Minute), []PoolView{{ID: 0, Active: true}}); act {
		t.Fatal("one pool, no switch")
	}
	if (FailoverCfg{DeadFor: time.Second, Cooldown: time.Minute, ReturnAfter: time.Minute}).Validate() == nil {
		t.Fatal("sub-minute DeadFor must fail")
	}
}

func TestFailoverPicksLowestPriorityBackup(t *testing.T) {
	c := FailoverCfg{DeadFor: 5 * time.Minute, Cooldown: 5 * time.Minute, ReturnAfter: 15 * time.Minute}
	var f Failover
	t0 := time.Unix(1794642000, 0)
	pools := []PoolView{
		{ID: 0, Priority: 0, Active: true},
		{ID: 2, Priority: 2, Alive: true},
		{ID: 1, Priority: 1, Alive: true},
	}
	f.Step(c, t0, pools)
	to, act, _ := f.Step(c, t0.Add(5*time.Minute), pools)
	if !act || to.ID != 1 {
		t.Fatalf("wanted pool 1 (priority 1), got %+v %v", to, act)
	}
}
