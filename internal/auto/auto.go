// Package auto holds the decision logic for HashWatch's opt-in automations:
// the work-speed schedule and backup-pool failover. Everything here is pure
// (time is passed in) so it can be unit-tested; the app sends the actual miner
// commands.
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

// ---------------- backup pool failover ----------------

// PoolView is the miner's already-configured pools. Failover never adds, removes
// or rewrites URLs or credentials; it only picks among these with switchpool|N.
type PoolView struct {
	ID        int
	Priority  int
	Alive     bool
	Rejecting bool // CGMiner "Rejecting"/"Sick", or a high reject rate
	Active    bool // the pool the miner is currently hashing on
	URL       string
}

func (p PoolView) healthy() bool { return p.Alive && !p.Rejecting }

// FailoverCfg: switch away from the active pool after it has been dead or
// rejecting for DeadFor; wait at least Cooldown between switches; switch back
// to the top-priority (primary) pool once it has been healthy for ReturnAfter.
// Unverified on a real Nano 3: switchpool is a standard cgminer command, but
// Canaan's published Nano 3S / Avalon Q API only documents workmode and ledset.
type FailoverCfg struct {
	DeadFor     time.Duration
	Cooldown    time.Duration
	ReturnAfter time.Duration
}

func (c FailoverCfg) Validate() error {
	if c.DeadFor < time.Minute || c.DeadFor > 120*time.Minute {
		return errors.New("wait before switching must be 1–120 minutes")
	}
	if c.Cooldown < time.Minute || c.Cooldown > 120*time.Minute {
		return errors.New("cooldown must be 1–120 minutes")
	}
	if c.ReturnAfter < time.Minute || c.ReturnAfter > 24*time.Hour {
		return errors.New("wait before switching back must be 1–1440 minutes")
	}
	return nil
}

type Failover struct {
	failSince  time.Time
	mainOK     time.Time
	lastSwitch time.Time
}

// Step returns the already-configured pool to switch to, if any.
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
	unhealthy := active == nil || !active.healthy()
	if !unhealthy {
		f.failSince = time.Time{}
	} else if f.failSince.IsZero() {
		f.failSince = now
	}
	cooling := !f.lastSwitch.IsZero() && now.Sub(f.lastSwitch) < c.Cooldown
	if unhealthy && !f.failSince.IsZero() && now.Sub(f.failSince) >= c.DeadFor && !cooling {
		best := PoolView{Priority: 1 << 30}
		found := false
		for _, p := range pools {
			if p.healthy() && (active == nil || p.ID != active.ID) && p.Priority < best.Priority {
				best, found = p, true
			}
		}
		if found {
			f.lastSwitch, f.failSince, f.mainOK = now, time.Time{}, time.Time{}
			why := "active pool unreachable"
			if active != nil && active.Rejecting {
				why = "active pool rejecting shares"
			}
			return best, true, fmt.Sprintf("%s for %s", why, short(c.DeadFor))
		}
	}
	if !unhealthy && active.ID != main.ID {
		if main.healthy() {
			if f.mainOK.IsZero() {
				f.mainOK = now
			}
			if now.Sub(f.mainOK) >= c.ReturnAfter && !cooling {
				f.lastSwitch, f.mainOK = now, time.Time{}
				return main, true, fmt.Sprintf("main pool healthy again for %s", short(c.ReturnAfter))
			}
		} else {
			f.mainOK = time.Time{}
		}
	} else {
		f.mainOK = time.Time{}
	}
	return PoolView{}, false, ""
}

// ---------------- heat ----------------

// HotPoint is the temperature above which a miner counts as running hot: its own
// reported target (TarT in estats) when it gives one, since every Nano and room
// differs, else a conservative fallback. Used for the "running hot" alert only:
// HashWatch never changes the work speed for heat; the Nano has its own overheat protection.
const FallbackHotC = 90.0

func HotPoint(target float64) (hotC float64, fromMiner bool) {
	if target >= 40 && target <= 110 {
		return target, true
	}
	return FallbackHotC, false
}

func short(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}
