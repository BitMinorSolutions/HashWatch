package app

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/auto"
	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/source"
)

// Automations runs each miner's opt-in automations after every poll: the work
// speed schedule, backup-pool failover, and the
// light effects (status lamp, block celebration, best-share flash). Every action
// it takes is recorded and raised as an "Automations" alert, so nothing happens
// silently. Nothing runs in the read-only preview or for non-CGMiner sources.
type Automations struct {
	Cfg    *Config
	Poll   *Poller
	Alerts *Alerts
	Fan    *FanSync // re-apply Cooler after a scheduled work-mode change
	Locked bool
	Now    func() time.Time
	// Send runs a miner command (Poll.Command; replaceable in tests).
	Send func(id string, do func(source.Controller) (cgminer.Result, error)) (cgminer.Result, error)
	// Sync runs commands inline instead of in a goroutine (tests).
	Sync bool

	mu sync.Mutex
	st map[string]*autoState
}

type Action struct {
	T    int64  `json:"t"`
	Kind string `json:"kind"` // schedule failover light
	Text string `json:"text"`
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
}

type autoState struct {
	sched     auto.Scheduler
	fo        auto.Failover
	busy      bool
	actions   []Action
	lightSent *LightState // what we last set (nil = the user's own light is showing)
	lightAt   time.Time
	celebrate time.Time // flash gold until
	bestFlash time.Time // flash until
	lastBest  time.Time
	problem   string
}

var (
	goldLight  = LightState{Mode: "flash", Color: "#ffc400", Bright: 100}
	bestLight  = LightState{Mode: "flash", Color: "#40c8e0", Bright: 100}
	okLamp     = LightState{Mode: "fixed", Color: "#30d158", Bright: 60}
	badLamp    = LightState{Mode: "flash", Color: "#ff3b30", Bright: 100}
	celebFor   = 60 * time.Second
	bestFor    = 12 * time.Second
	bestEvery  = 10 * time.Minute
	lightEvery = 8 * time.Second
)

func NewAutomations(cfg *Config, p *Poller, a *Alerts, locked bool) *Automations {
	return &Automations{Cfg: cfg, Poll: p, Alerts: a, Locked: locked, Now: time.Now, Send: p.Command, st: map[string]*autoState{}}
}

func (a *Automations) state(id string) *autoState {
	s := a.st[id]
	if s == nil {
		s = &autoState{}
		a.st[id] = s
	}
	return s
}

// Event is called by Stats for "block" and "best" events.
func (a *Automations) Event(id, kind string) {
	m, ok := a.Cfg.minerByID(id)
	if !ok {
		return
	}
	now := a.Now()
	a.mu.Lock()
	s := a.state(id)
	switch kind {
	case "block":
		if m.Opts.Light.Celebrate {
			s.celebrate = now.Add(celebFor)
		}
	case "best":
		if Optional.BestFlash && m.Opts.Light.BestFlash && now.Sub(s.lastBest) >= bestEvery { // OPTIONAL:bestflash
			s.bestFlash, s.lastBest = now.Add(bestFor), now
		}
	}
	a.mu.Unlock()
}

// LightChanged forgets what the effects last set (the user just set the light by hand).
func (a *Automations) LightChanged(id string) {
	a.mu.Lock()
	a.state(id).lightSent = nil
	a.mu.Unlock()
}

// ResetSchedule makes the schedule re-apply on the next poll (after an edit).
func (a *Automations) ResetSchedule(id string) {
	a.mu.Lock()
	a.state(id).sched.Reset()
	a.mu.Unlock()
}

// Actions returns the recent actions for a miner (newest first) and live state.
func (a *Automations) View(id string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.state(id)
	acts := make([]Action, 0, len(s.actions))
	for i := len(s.actions) - 1; i >= 0; i-- {
		acts = append(acts, s.actions[i])
	}
	out := map[string]any{"actions": acts, "light_override": s.lightSent != nil,
		"problem": s.problem}
	return out
}

func (a *Automations) record(id string, act Action) {
	a.mu.Lock()
	s := a.state(id)
	s.actions = append(s.actions, act)
	if len(s.actions) > 40 {
		s.actions = s.actions[len(s.actions)-40:]
	}
	a.mu.Unlock()
}

type job struct {
	kind, text string
	do         func(source.Controller) (cgminer.Result, error)
	after      func(err error)
	alert      bool
}

// Observe runs the automations for one miner after a poll.
func (a *Automations) Observe(id, name string, snap *model.Snapshot) {
	if a.Locked || snap == nil {
		return
	}
	m, ok := a.Cfg.minerByID(id)
	if !ok || m.Kind != "cgminer" {
		return
	}
	now := a.Now()
	o := m.Opts
	a.mu.Lock()
	s := a.state(id)
	if s.busy {
		a.mu.Unlock()
		return
	}
	var jobs []job
	level := -1
	if snap.Control != nil && snap.Control.WorkLevel != nil {
		level = *snap.Control.WorkLevel
	} else if snap.Power.WorkLevel != nil {
		level = *snap.Power.WorkLevel
	}
	setLevel := func(kind string, l int, why string) job {
		name := auto.LevelNames[l]
		return job{kind: kind, alert: true, text: fmt.Sprintf("Work speed → %s (%s)", strings.ToUpper(name[:1])+name[1:], why),
			do: func(c source.Controller) (cgminer.Result, error) {
				res, err := c.SetWorkMode(l)
				if err == nil && res.OK && a.Fan != nil {
					a.Fan.ApplyIfCooler(c, id)
				}
				return res, err
			}}
	}
	if snap.OK {
		// 1. schedule: the owner's chosen speeds
		if o.Schedule.On {
			if want, act := s.sched.Step(o.Schedule.Rules, o.Schedule.Default, now); act {
				if l, ok := auto.LevelOf(want); ok && l != level {
					j := setLevel("schedule", l, "schedule")
					j.after = func(err error) {
						if err != nil {
							a.ResetSchedule(id)
						}
					}
					jobs = append(jobs, j)
				}
			}
		}
		// 2. backup pool failover: switchpool among already-configured pools only
		if o.Failover.On && len(jobs) == 0 && len(snap.Pools) > 1 {
			var pv []auto.PoolView
			for _, p := range snap.Pools {
				pv = append(pv, poolView(p))
			}
			if to, act, why := s.fo.Step(o.Failover.Cfg(), now, pv); act {
				n := to.ID
				jobs = append(jobs, job{kind: "failover", alert: true, text: fmt.Sprintf("Switched to pool %d %s (%s)", n, shortURL(to.URL), why),
					do: func(c source.Controller) (cgminer.Result, error) { return c.SwitchPool(n) }})
			}
		}
	}
	// 3. light effects
	s.problem = problemOf(snap)
	if lj, ok := a.lightJob(id, m, s, now); ok && len(jobs) == 0 {
		jobs = append(jobs, lj)
	}
	if len(jobs) == 0 {
		a.mu.Unlock()
		return
	}
	s.busy = true
	a.mu.Unlock()
	run := func() {
		for _, j := range jobs {
			_, err := a.Send(id, j.do)
			act := Action{T: a.Now().Unix(), Kind: j.kind, Text: j.text, OK: err == nil}
			if err != nil {
				act.Err = err.Error()
			}
			a.record(id, act)
			if j.after != nil {
				j.after(err)
			}
			if j.alert && a.Alerts != nil {
				al := Alert{Miner: id, MinerName: name, Kind: "auto", Level: "info", Title: "Automation · " + name, Body: j.text}
				if err != nil {
					al.Level, al.Body = "warn", j.text+" failed: "+err.Error()
				}
				a.Alerts.Fire(al)
			}
		}
		a.mu.Lock()
		s.busy = false
		a.mu.Unlock()
	}
	if a.Sync {
		run()
	} else {
		go run()
	}
}

func shortURL(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "stratum+tcp://"), "stratum+ssl://")
	if u == "" {
		return ""
	}
	return "(" + u + ")"
}

// rejectHeavy is a conservative floor: a pool reporting this reject rate, with
// enough shares to mean it, is treated as rejecting even if Status is still Alive.
const rejectHeavy = 50.0
const rejectSample = 10.0

func poolView(p model.Pool) auto.PoolView {
	st := strings.TrimSpace(p.Status)
	rejecting := strings.EqualFold(st, "Rejecting") || strings.EqualFold(st, "Sick")
	if p.RejectPct != nil && *p.RejectPct >= rejectHeavy {
		var n float64
		if p.Accepted != nil {
			n += *p.Accepted
		}
		if p.Rejected != nil {
			n += *p.Rejected
		}
		if n >= rejectSample {
			rejecting = true
		}
	}
	return auto.PoolView{ID: p.ID, Priority: p.Priority, URL: p.URL, Active: p.StratumActive,
		Alive: strings.EqualFold(st, "Alive") && !rejecting, Rejecting: rejecting}
}

// problemOf is the status lamp's reason for red ("" = healthy). An offline miner
// can't be reached, so its light can't be changed; that case is left alone.
func problemOf(snap *model.Snapshot) string {
	if snap == nil || !snap.OK {
		return "offline"
	}
	if hs := snap.Errors.HashStatus; hs != nil && *hs != 1 {
		return "not hashing"
	}
	if len(snap.Pools) > 0 {
		up := false
		for _, p := range snap.Pools {
			if p.StratumActive && (p.Status == "" || strings.EqualFold(p.Status, "Alive")) {
				up = true
			}
		}
		if !up {
			return "no pool"
		}
	}
	if t, ok := fnum(snap.Temps.Max); ok && auto.ChipUnsafe(t) {
		return "hot"
	}
	if t, ok := fnum(snap.Temps.Avg); ok {
		if tg, ok := fnum(snap.Temps.Target); ok && auto.AvgRunningHot(t, tg) {
			return "hot"
		}
	}
	return ""
}

// lightJob decides the light: celebration > best-share flash > status lamp > the user's own light.
func (a *Automations) lightJob(id string, m Miner, s *autoState, now time.Time) (job, bool) {
	lo := m.Opts.Light
	lo.Lamp = lo.Lamp && Optional.StatusLamp // OPTIONAL:lamp
	var want *LightState
	why := ""
	switch {
	case now.Before(s.celebrate):
		want, why = &goldLight, "block found! flashing gold"
	case now.Before(s.bestFlash):
		want, why = &bestLight, "new best share"
	case lo.Lamp && s.problem == "offline":
		return job{}, false // unreachable: nothing to send
	case lo.Lamp && s.problem != "":
		want, why = &badLamp, "status lamp: "+s.problem
	case lo.Lamp:
		l := okLamp
		want, why = &l, "status lamp: healthy"
	case s.lightSent != nil:
		if lo.Base == nil {
			s.lightSent = nil
			return job{}, false
		}
		want, why = lo.Base, "back to your light"
	}
	if want == nil {
		return job{}, false
	}
	restoring := want == lo.Base
	if restoring && s.lightSent == nil || !restoring && s.lightSent != nil && *s.lightSent == *want {
		return job{}, false // already showing
	}
	if now.Sub(s.lightAt) < lightEvery {
		return job{}, false
	}
	w := *want
	s.lightAt = now
	return job{kind: "light", text: "Light: " + why, alert: false,
		do: func(c source.Controller) (cgminer.Result, error) {
			eff := 1
			for i, n := range cgminer.LEDModes {
				if n == w.Mode {
					eff = i
				}
			}
			return c.SetLED(eff, w.Bright, 50, w.Color)
		},
		after: func(err error) {
			if err != nil {
				return
			}
			a.mu.Lock()
			if restoring {
				s.lightSent = nil
			} else {
				s.lightSent = &w
			}
			a.mu.Unlock()
		}}, true
}
