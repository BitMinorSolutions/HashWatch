package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/mining"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/source"
)

// DefaultPoll is the one poll interval for every source (direct port 4028 and remote preview).
const DefaultPoll = 5 * time.Second

// GentlePoll is used when Gentle mode is on, so another dashboard can share the miner's API.
const GentlePoll = 30 * time.Second

// Backoff after failed fetches: first failure waits 15 s, then 30 s, until one succeeds.
var backoff = []time.Duration{15 * time.Second, 30 * time.Second}

// History keeps every sample for the last hour, then thins older ones to a
// thinEvery-second average per bucket, for 24 h in total (~720 + 5,520 samples
// at 5 s polling). 6H and 24H charts should be labeled as such.
const (
	historyKeep = 24 * 3600
	fullRes     = 3600
	thinEvery   = 15
	historyMax  = fullRes/2 + (historyKeep-fullRes)/thinEvery + 64 // hard cap (2 s polling worst case)
)

type minerState struct {
	Latest  *model.Snapshot `json:"latest"`
	LastOK  int64           `json:"last_ok"`
	LastErr string          `json:"last_err"`
	History []model.Sample  `json:"history"`
	src     source.Source
	name    string
	stop    chan struct{}
	ctlMu   sync.Mutex // one control command at a time per miner
	lastCtl time.Time
	ledMemo *model.Control
	fails   int           // consecutive failed fetches (drives backoff)
	next    time.Duration // current wait before the next fetch
	nextAt  time.Time     // when the next fetch starts // last light settings we applied (for firmwares that don't report them)
}

type Poller struct {
	Every time.Duration
	// Base is the interval with Gentle mode off (the --poll value, default 5 s).
	Base time.Duration
	// OnPoll is called after every poll (outside the lock) with the last hour of history.
	OnPoll func(id, name string, snap *model.Snapshot, lastHour []model.Sample)
	// Mining is the rolling accepted-share log and block-height clock.
	Mining *mining.Log
	dir    string
	mu     sync.RWMutex
	m      map[string]*minerState
	wait   chan struct{}
}

func NewPoller(dir string, every time.Duration) *Poller {
	p := &Poller{Every: every, Base: every, dir: dir, m: map[string]*minerState{}, Mining: mining.NewLog(dir), wait: make(chan struct{})}
	go func() {
		for range time.Tick(time.Minute) {
			p.saveHistory()
		}
	}()
	return p
}

func srcFor(m Miner) source.Source {
	if m.Kind == "remote" {
		return &source.Remote{Base: m.RemoteURL, User: m.RemoteUser, Pass: m.RemotePass}
	}
	port := m.Port
	if port == 0 {
		port = 4028
	}
	return source.CGMiner{Addr: net.JoinHostPort(m.Host, strconv.Itoa(port))}
}

func (p *Poller) histFile(id string) string { return filepath.Join(p.dir, "history-"+id+".json") }

func (p *Poller) Start(m Miner) {
	st := &minerState{src: srcFor(m), name: m.Name, stop: make(chan struct{})}
	if b, err := os.ReadFile(p.histFile(m.ID)); err == nil {
		_ = json.Unmarshal(b, &st.History)
	}
	p.mu.Lock()
	if old := p.m[m.ID]; old != nil {
		close(old.stop)
	}
	p.m[m.ID] = st
	p.mu.Unlock()
	go func() {
		for {
			start := time.Now()
			wait := p.pollOnce(m.ID, st)
			// Fixed cadence (start to start), with a short minimum gap between reads.
			t := time.NewTimer(max(time.Until(start.Add(wait)), min(time.Second, p.Every)))
			select {
			case <-st.stop:
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
}

func (p *Poller) Stop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st := p.m[id]; st != nil {
		close(st.stop)
		delete(p.m, id)
		os.Remove(p.histFile(id))
	}
	if p.Mining != nil {
		p.Mining.Remove(id)
	}
}

// Halt stops reading a miner but keeps its history (the Mac switched to showing a Pi).
func (p *Poller) Halt(id string) {
	p.mu.Lock()
	st := p.m[id]
	delete(p.m, id)
	p.mu.Unlock()
	if st != nil {
		close(st.stop)
	}
	p.saveHistory()
}

// Running reports how many miners are being read.
func (p *Poller) Running() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.m)
}

// SetGentle turns Gentle mode on or off. On means poll every GentlePoll (or Base
// if that is already slower). Backoff stays "never faster than the current interval".
func (p *Poller) SetGentle(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.Every = max(GentlePoll, p.Base)
	} else {
		p.Every = p.Base
	}
}

// interval is the wait after a fetch: Every on success, then 15 s, 30 s, 30 s… while failing.
func (p *Poller) interval(fails int) time.Duration {
	if fails == 0 {
		return p.Every
	}
	b := backoff[min(fails, len(backoff))-1]
	return max(b, p.Every)
}

func (p *Poller) pollOnce(id string, st *minerState) time.Duration {
	snapStart := time.Now()
	snap := st.src.Fetch()
	p.record(id, st, snap)
	if p.Mining != nil {
		p.Mining.Observe(id, snap, time.Now().Unix())
	}
	p.mu.Lock()
	if snap.OK {
		st.fails = 0
	} else {
		st.fails++
	}
	st.next = p.interval(st.fails)
	wait := st.next
	st.nextAt = time.Now().Add(max(wait-time.Since(snapStart), min(time.Second, p.Every)))
	p.mu.Unlock()
	if p.OnPoll != nil {
		p.mu.RLock()
		var hr []model.Sample
		cut := time.Now().Unix() - 3700
		for _, h := range st.History {
			if h.T >= cut {
				hr = append(hr, h)
			}
		}
		p.mu.RUnlock()
		p.OnPoll(id, st.name, snap, hr)
	}
	return wait
}

func (p *Poller) record(id string, st *minerState, snap *model.Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, m := snap.Control, st.ledMemo; c != nil && m != nil && c.LEDHex == "" {
		c.LEDHex, c.LEDBright, c.LEDTemper = m.LEDHex, m.LEDBright, m.LEDTemper
	}
	st.Latest = snap
	if snap.OK {
		st.LastOK, st.LastErr = time.Now().Unix(), ""
		s := snap.Sample()
		if n := len(st.History); n == 0 || st.History[n-1].T != s.T {
			st.History = append(st.History, s)
		}
		if n := len(st.History); n%60 == 0 || n > historyMax {
			st.History = thin(st.History, s.T)
		}
		if len(st.History) > historyMax {
			st.History = st.History[len(st.History)-historyMax:]
		}
	} else {
		st.LastErr = fmt.Sprint(snap.QueryErrors)
		log.Printf("miner %s: %s", id, st.LastErr)
	}
	if p.wait != nil {
		close(p.wait)
		p.wait = make(chan struct{})
	}
}

// thin drops samples older than 24 h. Beyond the last hour it keeps one point
// per thinEvery seconds, averaging hashrate, temperature, power and fan in
// each bucket (share counters keep the last value in the bucket).
func thin(h []model.Sample, now int64) []model.Sample {
	out := make([]model.Sample, 0, len(h))
	var bucket []model.Sample
	flush := func() {
		if len(bucket) == 0 {
			return
		}
		out = append(out, averageSamples(bucket))
		bucket = bucket[:0]
	}
	for _, x := range h {
		if x.T < now-historyKeep {
			continue
		}
		if x.T >= now-fullRes {
			flush()
			out = append(out, x)
			continue
		}
		if len(bucket) > 0 && x.T-bucket[0].T < thinEvery {
			bucket = append(bucket, x)
			continue
		}
		flush()
		bucket = append(bucket, x)
	}
	flush()
	return out
}

func averageSamples(b []model.Sample) model.Sample {
	if len(b) == 1 {
		return b[0]
	}
	out := b[len(b)-1]
	avg := func(get func(model.Sample) *float64, set func(*float64)) {
		var sum float64
		n := 0
		for _, s := range b {
			if p := get(s); p != nil {
				sum += *p
				n++
			}
		}
		if n == 0 {
			set(nil)
			return
		}
		v := sum / float64(n)
		set(&v)
	}
	avg(func(s model.Sample) *float64 { return s.HHero }, func(v *float64) { out.HHero = v })
	avg(func(s model.Sample) *float64 { return s.H5s }, func(v *float64) { out.H5s = v })
	avg(func(s model.Sample) *float64 { return s.H1m }, func(v *float64) { out.H1m = v })
	avg(func(s model.Sample) *float64 { return s.H5m }, func(v *float64) { out.H5m = v })
	avg(func(s model.Sample) *float64 { return s.H15m }, func(v *float64) { out.H15m = v })
	avg(func(s model.Sample) *float64 { return s.TMax }, func(v *float64) { out.TMax = v })
	avg(func(s model.Sample) *float64 { return s.TAvg }, func(v *float64) { out.TAvg = v })
	avg(func(s model.Sample) *float64 { return s.TAmb }, func(v *float64) { out.TAmb = v })
	avg(func(s model.Sample) *float64 { return s.TOut }, func(v *float64) { out.TOut = v })
	avg(func(s model.Sample) *float64 { return s.RPM }, func(v *float64) { out.RPM = v })
	avg(func(s model.Sample) *float64 { return s.W }, func(v *float64) { out.W = v })
	return out
}

type LiveView struct {
	Latest  *model.Snapshot `json:"data"`
	LastOK  int64           `json:"last_ok"`
	LastErr string          `json:"last_err"`
	History []model.Sample  `json:"history"`
	Poll    int             `json:"poll_seconds"` // current interval, including any backoff
	NextAt  float64         `json:"next_poll_unix,omitempty"`
}

func (p *Poller) Live(id string, since int64) (LiveView, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := p.m[id]
	if st == nil {
		return LiveView{}, false
	}
	v := LiveView{Latest: st.Latest, LastOK: st.LastOK, LastErr: st.LastErr, Poll: int(p.interval(st.fails) / time.Second)}
	if !st.nextAt.IsZero() {
		v.NextAt = float64(st.nextAt.UnixMilli()) / 1000
	}
	for _, h := range st.History {
		if h.T > since {
			v.History = append(v.History, h)
		}
	}
	return v, true
}

// HistorySince returns stored samples at or after since (unix seconds),
// including the full 24 h window the poller keeps.
func (p *Poller) HistorySince(id string, since int64) []model.Sample {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := p.m[id]
	if st == nil {
		return nil
	}
	var out []model.Sample
	for _, h := range st.History {
		if h.T >= since {
			out = append(out, h)
		}
	}
	return out
}

func (p *Poller) MiningView(id string, since int64) mining.View {
	if p == nil || p.Mining == nil {
		return mining.View{}
	}
	return p.Mining.View(id, since)
}

// lastChangeUnix is the most recent poll timestamp we can wait on (ok or failed).
func (p *Poller) lastChangeUnix(id string) int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := p.m[id]
	if st == nil || st.Latest == nil {
		return 0
	}
	if st.Latest.FetchedAt > 0 {
		return st.Latest.FetchedAt
	}
	return st.LastOK
}

// WaitChange returns when this miner has a newer sample than since, or ctx is done.
func (p *Poller) WaitChange(ctx context.Context, id string, since int64) {
	for {
		if p.lastChangeUnix(id) > since {
			return
		}
		p.mu.RLock()
		ch := p.wait
		p.mu.RUnlock()
		if ch == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ch:
		}
	}
}

// SaveNow writes history to disk (used on shutdown).
func (p *Poller) SaveNow() { p.saveHistory() }

func (p *Poller) saveHistory() {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, st := range p.m {
		b, _ := json.Marshal(st.History)
		_ = os.WriteFile(p.histFile(id), b, 0o600)
	}
	if p.Mining != nil {
		p.Mining.Save()
	}
}

var ErrNoControl = fmt.Errorf("this miner can't be controlled from here")

// Control runs one control action against a direct (CGMiner) miner, then re-reads it.
func (p *Poller) Control(id string, do func(source.Controller) (cgminer.Result, error), memo *model.Control) (cgminer.Result, *model.Snapshot, error) {
	p.mu.RLock()
	st := p.m[id]
	p.mu.RUnlock()
	if st == nil {
		return cgminer.Result{}, nil, fmt.Errorf("unknown miner")
	}
	ctl, ok := st.src.(source.Controller)
	if !ok {
		return cgminer.Result{}, nil, ErrNoControl
	}
	if !st.ctlMu.TryLock() {
		return cgminer.Result{}, nil, fmt.Errorf("another change is still being applied")
	}
	defer st.ctlMu.Unlock()
	if wait := 3*time.Second - time.Since(st.lastCtl); wait > 0 {
		return cgminer.Result{}, nil, fmt.Errorf("wait a few seconds between changes")
	}
	st.lastCtl = time.Now()
	res, err := do(ctl)
	if err == nil && res.OK && memo != nil {
		p.mu.Lock()
		st.ledMemo = memo
		p.mu.Unlock()
	}
	log.Printf("control %s: sent %q -> ok=%v status=%s msg=%q err=%v", id, res.Sent, res.OK, res.Status, res.Message, err)
	time.Sleep(1500 * time.Millisecond) // let the miner apply it before reading back
	p.pollOnce(id, st)
	p.mu.RLock()
	snap := st.Latest
	p.mu.RUnlock()
	return res, snap, err
}

// cmdGap is the minimum time between two commands to the same miner.
var cmdGap = 3 * time.Second

// Command runs one automation command against a direct miner without the
// read-back delay of Control (the next regular poll reads the result).
func (p *Poller) Command(id string, do func(source.Controller) (cgminer.Result, error)) (cgminer.Result, error) {
	p.mu.RLock()
	st := p.m[id]
	p.mu.RUnlock()
	if st == nil {
		return cgminer.Result{}, fmt.Errorf("unknown miner")
	}
	ctl, ok := st.src.(source.Controller)
	if !ok {
		return cgminer.Result{}, ErrNoControl
	}
	if !st.ctlMu.TryLock() {
		return cgminer.Result{}, fmt.Errorf("another change is still being applied")
	}
	defer st.ctlMu.Unlock()
	if wait := cmdGap - time.Since(st.lastCtl); wait > 0 {
		time.Sleep(wait)
	}
	st.lastCtl = time.Now()
	res, err := do(ctl)
	log.Printf("automation %s: sent %q -> ok=%v status=%s msg=%q err=%v", id, res.Sent, res.OK, res.Status, res.Message, err)
	if err == nil && !res.OK {
		err = fmt.Errorf("the miner refused it: %s", res.Message)
	}
	return res, err
}

// Latest returns a miner's most recent snapshot (nil if none yet).
func (p *Poller) Latest(id string) *model.Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if st := p.m[id]; st != nil {
		return st.Latest
	}
	return nil
}
