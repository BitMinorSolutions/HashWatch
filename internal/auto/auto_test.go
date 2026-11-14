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
