package app

import (
	"encoding/json"
	"fmt"
	"github.com/hashwatch/hashwatch/internal/auto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

type Alert struct {
	ID        int64  `json:"id"`
	T         int64  `json:"t"`
	Miner     string `json:"miner,omitempty"`
	MinerName string `json:"miner_name,omitempty"`
	Kind      string `json:"kind"`
	Level     string `json:"level"` // celebrate, good, warn, bad, info
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url,omitempty"`
}

type AlertKind struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Desc  string `json:"desc"`
}

var AlertKinds = []AlertKind{
	{"block", "Block found", "When the miner's Found Blocks count goes up."},
	{"offline", "Offline / back online", "When " + AppName + " can't reach a miner for 30 seconds, and when it's back."},
	{"hashrate", "Hashrate drop", "Hashrate more than 30% below its 1-hour average for 10 minutes."},
	{"temp", "Running hot", "Max chip temperature above the miner's own target (or 90 °C if it reports none) for 5 minutes."},
	{"pool", "Pool disconnected", "When no pool is connected, and when it reconnects."},
	{"link", "Share link changed", "When your share link changes, so you can send the new one."},
	{"nearmiss", "Near misses & best shares", "A new personal-best share, or any share above your near-miss threshold."},
	{"badge", "Badges", "When you earn a badge (streaks, big shares, ticket milestones)."},
	{"summary", "Daily / weekly summary", "Average hashrate, time online, best share and tickets for the day or week."},
	{"auto", "Automations", "Whenever the schedule, backup-pool failover or a light effect changes something on the miner."},
}

// Alert thresholds (vars so tests can shorten them).
var (
	offlineAfter  = 2 // consecutive failed polls (~15-20 s: a failure, then a retry 15 s later)
	hashDropFor   = 10 * time.Minute
	hashDropRatio = 0.70
	hashClear     = 0.80
	hotFor        = 5 * time.Minute
	poolDownPolls = 2
)

type alertState struct {
	seenOK, offline     bool
	fails               int
	blocks              float64
	haveBlocks          bool
	lowSince            time.Time
	lowBase             float64
	lowFired            bool
	hotSince            time.Time
	hotFired            bool
	poolDownN           int
	poolDown, poolKnown bool
}

type Alerts struct {
	Enabled func(kind string) bool
	Notify  func(Alert)
	Push    func(Alert) // optional (ntfy); never include credentials in the payload
	Now     func() time.Time

	mu    sync.Mutex
	path  string
	items []Alert
	seq   int64
	st    map[string]*alertState
}

const alertsMax = 300

func NewAlerts(dir string) *Alerts {
	a := &Alerts{path: filepath.Join(dir, "alerts.json"), st: map[string]*alertState{}, Now: time.Now}
	if b, err := os.ReadFile(a.path); err == nil {
		_ = json.Unmarshal(b, &a.items)
	}
	for _, it := range a.items {
		if it.ID > a.seq {
			a.seq = it.ID
		}
	}
	return a
}

// Since returns alerts newer than id (oldest first) and the latest id.
func (a *Alerts) Since(id int64, limit int) ([]Alert, int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Alert
	for _, it := range a.items {
		if it.ID > id {
			out = append(out, it)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, a.seq
}

func (a *Alerts) Clear() {
	a.mu.Lock()
	a.items = nil
	a.mu.Unlock()
	a.save()
}

func (a *Alerts) save() {
	a.mu.Lock()
	b, _ := json.Marshal(a.items)
	a.mu.Unlock()
	_ = os.WriteFile(a.path, b, 0o600)
}

// Fire records an alert (if its kind is on) and sends the desktop notification.
func (a *Alerts) Fire(al Alert) (Alert, bool) {
	if a.Enabled != nil && !a.Enabled(al.Kind) && al.Kind != "test" {
		return al, false
	}
	a.mu.Lock()
	a.seq++
	if ns := a.Now().UnixMilli(); ns > a.seq { // ids stay unique and increasing across restarts
		a.seq = ns
	}
	al.ID, al.T = a.seq, a.Now().Unix()
	a.items = append(a.items, al)
	if len(a.items) > alertsMax {
		a.items = a.items[len(a.items)-alertsMax:]
	}
	a.mu.Unlock()
	a.save()
	if a.Notify != nil {
		go a.Notify(al)
	}
	if a.Push != nil {
		go a.Push(al)
	}
	return al, true
}

func fnum(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

func plural(n float64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Observe updates the alert state for one miner after a poll.
func (a *Alerts) Observe(id, name string, snap *model.Snapshot, hist []model.Sample) {
	a.mu.Lock()
	s := a.st[id]
	if s == nil {
		s = &alertState{}
		a.st[id] = s
	}
	a.mu.Unlock()
	now := a.Now()
	base := Alert{Miner: id, MinerName: name}
	fire := func(kind, level, title, body string) {
		al := base
		al.Kind, al.Level, al.Title, al.Body = kind, level, title, body
		a.Fire(al)
	}
	if snap == nil || !snap.OK {
		s.fails++
		if s.fails >= offlineAfter && s.seenOK && !s.offline {
			s.offline = true
			fire("offline", "bad", name+" is offline", AppName+" can't reach the miner. Check its power and network.")
		}
		s.lowSince, s.hotSince = time.Time{}, time.Time{}
		return
	}
	if s.offline {
		fire("offline", "good", name+" is back online", "The miner is answering again.")
	}
	s.offline, s.fails, s.seenOK = false, 0, true

	if v, ok := fnum(snap.Shares.FoundBlocks); ok {
		if s.haveBlocks && v > s.blocks {
			n := v - s.blocks
			fire("block", "celebrate", "Block found!", fmt.Sprintf("Found Blocks went from %.0f to %.0f%s. Check your pool or wallet for the reward.",
				s.blocks, v, plural(n, "", fmt.Sprintf(" (%.0f new)", n))))
		}
		s.blocks, s.haveBlocks = v, true
	}

	// Hashrate drop: 5-minute average vs. the trailing 1-hour mean.
	if cur, ok := fnum(snap.Hashrate.THs5m); ok {
		var sum float64
		var n int
		var first int64
		for _, h := range hist {
			if h.T >= now.Unix()-3600 && h.H5m != nil {
				if n == 0 {
					first = h.T
				}
				sum += *h.H5m
				n++
			}
		}
		if n > 0 && now.Unix()-first >= 30*60 { // need at least 30 min of history
			avg := sum / float64(n)
			switch {
			case s.lowSince.IsZero() && cur < hashDropRatio*avg:
				s.lowSince, s.lowBase = now, avg
			case !s.lowSince.IsZero() && cur >= hashClear*s.lowBase:
				s.lowSince, s.lowFired = time.Time{}, false
			}
			if !s.lowSince.IsZero() && !s.lowFired && now.Sub(s.lowSince) >= hashDropFor {
				s.lowFired = true
				fire("hashrate", "warn", "Hashrate dropped on "+name,
					fmt.Sprintf("%.2f TH/s for the last %d minutes, %.0f%% below the 1-hour average of %.2f TH/s.", cur,
						int(now.Sub(s.lowSince).Minutes()), (1-cur/s.lowBase)*100, s.lowBase))
			}
		}
	}

	// Temperature over target.
	if t, ok := fnum(snap.Temps.Max); ok {
		tgRaw, _ := fnum(snap.Temps.Target)
		if tg, fromMiner := auto.HotPoint(tgRaw); t > tg {
			what := "target"
			if !fromMiner {
				what = "safe limit"
			}
			if s.hotSince.IsZero() {
				s.hotSince = now
			}
			if !s.hotFired && now.Sub(s.hotSince) >= hotFor {
				s.hotFired = true
				fire("temp", "warn", name+" is running hot", fmt.Sprintf("Max chip %.0f°C has been above the %.0f°C %s for %d minutes. Check airflow or lower the work speed.",
					t, tg, what, int(now.Sub(s.hotSince).Minutes())))
			}
		} else {
			s.hotSince, s.hotFired = time.Time{}, false
		}
	}

	// Pool connection.
	if len(snap.Pools) > 0 {
		up := false
		for _, p := range snap.Pools {
			if p.StratumActive && (p.Status == "" || strings.EqualFold(p.Status, "Alive")) {
				up = true
			}
		}
		if up {
			if s.poolDown {
				fire("pool", "good", "Pool reconnected", name+" is connected to its pool again.")
			}
			s.poolDown, s.poolDownN, s.poolKnown = false, 0, true
		} else {
			s.poolDownN++
			if s.poolKnown && !s.poolDown && s.poolDownN >= poolDownPolls {
				s.poolDown = true
				fire("pool", "bad", "Pool disconnected", name+" isn't connected to any pool, so it isn't earning. Check the pool address and your internet.")
			}
		}
	}
}
