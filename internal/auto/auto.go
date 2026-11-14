// Package auto holds the decision logic for HashWatch's opt-in automations:
// the work-speed schedule, auto-throttle when hot, backup-pool failover and
// auto-restart on a stalled hashrate. Everything here is pure (time is passed
// in) so it can be unit-tested; the app sends the actual miner commands.
package auto

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Levels are the Nano 3 work speeds: 0 Low, 1 Medium, 2 High.
var LevelNames = []string{"low", "medium", "high"}

func LevelOf(name string) (int, bool) {
	for i, n := range LevelNames {
		if strings.EqualFold(n, name) {
			return i, true
		}
	}
	return 0, false
}

// ---------------- schedule ----------------

// Rule runs a work speed on some weekdays between Start and End (local time).
// End before Start means the rule runs overnight into the next day.
type Rule struct {
	Days  []int  `json:"days"`  // 0 = Sunday … 6 = Saturday (the day the rule starts)
	Start string `json:"start"` // "HH:MM"
	End   string `json:"end"`   // "HH:MM"
	Mode  string `json:"mode"`  // low | medium | high
}

func hm(s string) (int, error) {
	p := strings.Split(strings.TrimSpace(s), ":")
	if len(p) != 2 {
		return 0, fmt.Errorf("time %q must look like 22:00", s)
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	if e1 != nil || e2 != nil || h < 0 || h > 24 || m < 0 || m > 59 || h == 24 && m != 0 {
		return 0, fmt.Errorf("time %q must look like 22:00", s)
	}
	return h*60 + m, nil
}

// Validate checks one rule.
func (r Rule) Validate() error {
	if len(r.Days) == 0 {
		return errors.New("pick at least one day")
	}
	for _, d := range r.Days {
		if d < 0 || d > 6 {
			return errors.New("days are 0 (Sunday) to 6 (Saturday)")
		}
	}
	s, err := hm(r.Start)
	if err != nil {
		return err
	}
	e, err := hm(r.End)
	if err != nil {
		return err
	}
	if s == e {
		return errors.New("start and end can't be the same time")
	}
	if _, ok := LevelOf(r.Mode); !ok {
		return errors.New("mode must be low, medium or high")
	}
	return nil
}

func hasDay(days []int, d int) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

// Active reports whether rule r covers time t.
func (r Rule) Active(t time.Time) bool {
	s, err1 := hm(r.Start)
	e, err2 := hm(r.End)
	if err1 != nil || err2 != nil || s == e {
		return false
	}
	min := t.Hour()*60 + t.Minute()
	wd := int(t.Weekday())
	if s < e {
		return hasDay(r.Days, wd) && min >= s && min < e
	}
	// overnight: the evening part on a rule day, the morning part on the following day
	return hasDay(r.Days, wd) && min >= s || hasDay(r.Days, (wd+6)%7) && min < e
}

// Desired returns the work speed the schedule wants at t: the first matching
// rule wins; with no match it is def ("" = leave the miner alone).
func Desired(rules []Rule, def string, t time.Time) (mode string, rule int) {
	for i, r := range rules {
		if r.Active(t) {
			return strings.ToLower(r.Mode), i
		}
	}
	return strings.ToLower(def), -1
}

// Scheduler applies the schedule only when what it wants changes, so a manual
// change sticks until the next schedule boundary.
type Scheduler struct{ last string }

// Step returns the mode to apply now, if any.
func (s *Scheduler) Step(rules []Rule, def string, t time.Time) (mode string, act bool) {
	want, _ := Desired(rules, def, t)
	if want == s.last {
		return "", false
	}
	s.last = want
	if want == "" {
		return "", false
	}
	return want, true
}

// Reset forgets the last applied mode (e.g. after the schedule is edited).
func (s *Scheduler) Reset() { s.last = "" }

// ---------------- auto-throttle ----------------

// ThrottleCfg: step down one work speed after the max chip temperature has been
// at or above HotC for Hold; step back up (one level at a time, never above the
// level it started from) after it has been at or below CoolC for Cool. Between
// CoolC and HotC nothing changes (hysteresis), and steps are at least Gap apart.
type ThrottleCfg struct {
	HotC, CoolC     float64
	Hold, Cool, Gap time.Duration
}

func (c ThrottleCfg) Validate() error {
	if c.HotC <= 0 || c.CoolC <= 0 || c.CoolC >= c.HotC {
		return errors.New("the cool-down temperature must be below the hot temperature")
	}
	if c.HotC > 110 || c.CoolC < 30 {
		return errors.New("temperatures must be between 30 and 110 °C")
	}
	return nil
}

type Throttle struct {
	Steps     int // levels currently taken off
	Restore   int // level to return to
	hotSince  time.Time
	coolSince time.Time
	lastStep  time.Time
	expect    int
}

// Throttled reports whether the throttle currently holds the miner below its level.
func (t *Throttle) Throttled() bool { return t.Steps > 0 }

// Step returns the new level to apply (act) and why.
func (t *Throttle) Step(c ThrottleCfg, now time.Time, tmax float64, level int) (newLevel int, act bool, reason string) {
	// someone changed the speed by hand while throttled: they take over
	if t.Steps > 0 && level != t.expect && now.Sub(t.lastStep) > 45*time.Second {
		*t = Throttle{}
	}
	gapOK := t.lastStep.IsZero() || now.Sub(t.lastStep) >= c.Gap
	switch {
	case tmax >= c.HotC:
		t.coolSince = time.Time{}
		if t.hotSince.IsZero() {
			t.hotSince = now
		}
		if now.Sub(t.hotSince) >= c.Hold && level > 0 && gapOK {
			if t.Steps == 0 {
				t.Restore = level
			}
			t.Steps++
			t.lastStep, t.expect = now, level-1
			t.hotSince = now // the next step needs another full Hold
			return level - 1, true, fmt.Sprintf("max chip %.0f°C ≥ %.0f°C for %s", tmax, c.HotC, short(c.Hold))
		}
	case tmax <= c.CoolC && t.Steps > 0:
		t.hotSince = time.Time{}
		if t.coolSince.IsZero() {
			t.coolSince = now
		}
		if now.Sub(t.coolSince) >= c.Cool && gapOK {
			nl := min(level+1, t.Restore)
			t.Steps--
			if nl >= t.Restore {
				t.Steps = 0
			}
			t.lastStep, t.expect, t.coolSince = now, nl, now
			if nl == level {
				return level, false, ""
			}
			return nl, true, fmt.Sprintf("max chip %.0f°C ≤ %.0f°C for %s", tmax, c.CoolC, short(c.Cool))
		}
	default:
		t.hotSince, t.coolSince = time.Time{}, time.Time{}
	}
	return level, false, ""
}

// SetRestore changes the level a throttled miner returns to (the schedule moved on).
func (t *Throttle) SetRestore(level int) {
	if t.Steps > 0 {
		t.Restore = level
	}
}

func short(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

// ---------------- backup pool failover ----------------

type PoolView struct {
	ID       int
	Priority int
	Alive    bool
	Active   bool // the pool the miner is hashing on
	URL      string
}

type FailoverCfg struct {
	DeadFor      time.Duration // active pool dead this long → switch
	ReturnToMain bool          // go back to the top-priority pool once it's healthy again
	ReturnAfter  time.Duration
}

type Failover struct {
	deadSince  time.Time
	mainOK     time.Time
	lastSwitch time.Time
}

// Step returns the pool to switch to, if any.
func (f *Failover) Step(c FailoverCfg, now time.Time, pools []PoolView) (to PoolView, act bool, reason string) {
	if len(pools) < 2 {
		return PoolView{}, false, ""
	}
	var active *PoolView
	main := pools[0]
	for i := range pools {
		p := &pools[i]
		if p.Active && active == nil {
			active = p
		}
		if p.Priority < main.Priority {
			main = *p
		}
	}
	healthy := active != nil && active.Alive
	if healthy {
		f.deadSince = time.Time{}
	} else if f.deadSince.IsZero() {
		f.deadSince = now
	}
	recent := !f.lastSwitch.IsZero() && now.Sub(f.lastSwitch) < max(c.DeadFor, 2*time.Minute)
	if !healthy && now.Sub(f.deadSince) >= c.DeadFor && !recent {
		best := PoolView{Priority: 1 << 30}
		found := false
		for _, p := range pools {
			if p.Alive && (active == nil || p.ID != active.ID) && p.Priority < best.Priority {
				best, found = p, true
			}
		}
		if found {
			f.lastSwitch, f.deadSince = now, time.Time{}
			return best, true, fmt.Sprintf("active pool unreachable for %s", short(c.DeadFor))
		}
	}
	if c.ReturnToMain && healthy && active.ID != main.ID {
		if main.Alive {
			if f.mainOK.IsZero() {
				f.mainOK = now
			}
			if now.Sub(f.mainOK) >= c.ReturnAfter && !recent {
				f.lastSwitch, f.mainOK = now, time.Time{}
				return main, true, fmt.Sprintf("main pool healthy again for %s", short(c.ReturnAfter))
			}
		} else {
			f.mainOK = time.Time{}
		}
	}
	return PoolView{}, false, ""
}

// ---------------- auto-restart on stall ----------------

type RestartCfg struct {
	BelowTHs  float64       // "stalled" means the 1-minute hashrate is below this
	For       time.Duration // for this long, while the miner answers
	Cooldown  time.Duration // at least this long between restarts
	MaxPerDay int           // never more than this many in 24 hours
}

type Restarter struct {
	lowSince time.Time
	History  []time.Time
}

// Step decides whether to restart now. online means the miner answered this poll.
func (r *Restarter) Step(c RestartCfg, now time.Time, online bool, ths float64) (act bool, reason string) {
	if !online || ths >= c.BelowTHs {
		r.lowSince = time.Time{}
		return false, ""
	}
	if r.lowSince.IsZero() {
		r.lowSince = now
	}
	if now.Sub(r.lowSince) < c.For {
		return false, ""
	}
	var day []time.Time
	for _, t := range r.History {
		if now.Sub(t) < 24*time.Hour {
			day = append(day, t)
		}
	}
	r.History = day
	if len(day) >= c.MaxPerDay {
		return false, ""
	}
	if n := len(day); n > 0 && now.Sub(day[n-1]) < c.Cooldown {
		return false, ""
	}
	r.History = append(r.History, now)
	r.lowSince = time.Time{}
	return true, fmt.Sprintf("hashrate %.2f TH/s below %.2f TH/s for %s", ths, c.BelowTHs, short(c.For))
}
