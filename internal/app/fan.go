package app

import (
	"sync"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/source"
)

// FanSync re-applies a persisted Cooler fan speed at HashWatch start, after a
// miner reboot (Elapsed went down), and after a work-mode change HashWatch
// itself makes. It does not poll-and-override, and it never chases temperature.
// Auto is a one-shot -1 when the user chooses it, not re-applied.
type FanSync struct {
	Cfg  *Config
	Poll *Poller

	mu sync.Mutex
	st map[string]*fanState
}

type fanState struct {
	elapsed     int64
	haveElapsed bool
	pending     bool
}

func NewFanSync(cfg *Config, poll *Poller) *FanSync {
	f := &FanSync{Cfg: cfg, Poll: poll, st: map[string]*fanState{}}
	if cfg == nil {
		return f
	}
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	for _, m := range cfg.Miners {
		if m.Opts.Fan.Cooler() {
			f.st[m.ID] = &fanState{pending: true}
		}
	}
	return f
}

func (f *FanSync) opts(id string) FanOpts {
	if f == nil || f.Cfg == nil {
		return FanOpts{}
	}
	m, ok := f.Cfg.minerByID(id)
	if !ok {
		return FanOpts{}
	}
	return m.Opts.Fan
}

func (f *FanSync) state(id string) *fanState {
	if f.st == nil {
		f.st = map[string]*fanState{}
	}
	st := f.st[id]
	if st == nil {
		st = &fanState{}
		f.st[id] = st
	}
	return st
}

func (f *FanSync) MarkPending(id string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state(id).pending = true
}

func (f *FanSync) ClearPending(id string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state(id).pending = false
}

// Observe is called after a poll. On a successful read, it records Elapsed and
// sends Cooler N once if this is the first successful poll since start (or a
// restart), Elapsed went down (miner reboot), or a previous apply is still pending.
func (f *FanSync) Observe(id string, snap *model.Snapshot) {
	if f == nil || snap == nil || !snap.OK || !cgminer.SupportsFanSpd(snap) {
		return
	}
	if !f.opts(id).Cooler() {
		f.mu.Lock()
		st := f.state(id)
		st.elapsed = snap.Uptime.Seconds
		st.haveElapsed = true
		st.pending = false
		f.mu.Unlock()
		return
	}
	f.mu.Lock()
	st := f.state(id)
	reboot := st.haveElapsed && snap.Uptime.Seconds < st.elapsed
	apply := st.pending || reboot
	st.elapsed = snap.Uptime.Seconds
	st.haveElapsed = true
	if apply {
		st.pending = true
	}
	f.mu.Unlock()
	if !apply {
		return
	}
	if err := f.sendCooler(id); err != nil {
		return
	}
	f.ClearPending(id)
}

// ApplyIfCooler sends the persisted Cooler speed on an already-held controller
// (after a work-mode change). It must not call Poll.Command: the caller already
// holds the per-miner control lock.
func (f *FanSync) ApplyIfCooler(c source.Controller, id string) {
	if f == nil || c == nil || !f.opts(id).Cooler() {
		return
	}
	if f.Poll != nil {
		if snap := f.Poll.Latest(id); snap != nil && !cgminer.SupportsFanSpd(snap) {
			return
		}
	}
	_, err := c.SetFanSpd(f.opts(id).Speed())
	if err != nil {
		f.MarkPending(id)
		return
	}
	f.ClearPending(id)
}

func (f *FanSync) sendCooler(id string) error {
	if f.Poll == nil {
		return nil
	}
	speed := f.opts(id).Speed()
	_, err := f.Poll.Command(id, func(c source.Controller) (cgminer.Result, error) {
		return c.SetFanSpd(speed)
	})
	return err
}

func (s *Server) fanView(id string, snap *model.Snapshot) map[string]any {
	mode, pct := "auto", cgminer.FanDefault
	if m, ok := s.Cfg.minerByID(id); ok {
		if m.Opts.Fan.Cooler() {
			mode = "cooler"
			pct = m.Opts.Fan.Speed()
		} else if m.Opts.Fan.Percent >= cgminer.FanMinPct && m.Opts.Fan.Percent <= cgminer.FanMaxPct {
			pct = m.Opts.Fan.Percent
		}
	}
	return map[string]any{
		"supported": cgminer.SupportsFanSpd(snap),
		"mode":      mode,
		"percent":   pct,
	}
}
