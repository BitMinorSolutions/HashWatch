// Package auto holds the decision logic for HashWatch's opt-in automations:
// the work-speed schedule. Everything here is pure (time is passed
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
