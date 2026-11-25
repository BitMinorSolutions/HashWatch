package mining

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

// Round counting: "Guesses this round" is the miner's own work counter
// (cgminer summary "Total MH") now, minus a snapshot of that counter taken when
// HashWatch saw the current network block arrive. Everything here only reads
// data HashWatch already polls; it never sends the miner anything.

// RoundOfflineGap is how long HashWatch can go without trying a poll before it
// counts as "HashWatch was offline" (the slowest normal cadence is 30 s).
var RoundOfflineGap = 90 * time.Second

// roundUptimeSlack is how far the miner's uptime may lag the wall clock between
// two polls before HashWatch decides the miner restarted in between.
const roundUptimeSlack = 20

// Why a round is only partly counted.
const (
	PartialOffline     = "offline"     // HashWatch wasn't running/polling when the block arrived or the miner restarted
	PartialUnreachable = "unreachable" // the miner wasn't answering when the block arrived
)

// Restart is one miner restart seen during a round.
type Restart struct {
	T       int64 `json:"t"`                 // when the miner came back up (unix, from its uptime)
	DownSec int64 `json:"down_s"`            // seconds between the last good read and the restart that the count can't include
	Unknown bool  `json:"unknown,omitempty"` // HashWatch was offline, so work before the restart is unknown
}

type roundState struct {
	Height     int64     `json:"height"`             // tip block the round started after
	StartT     int64     `json:"start_t"`            // when HashWatch took the snapshot
	StartMH    float64   `json:"start_mh"`           // Total MH at the snapshot (current miner boot)
	CarryMH    float64   `json:"carry_mh,omitempty"` // MH counted this round in earlier boots
	Partial    bool      `json:"partial,omitempty"`
	PartialWhy string    `json:"partial_why,omitempty"`
	Restarts   []Restart `json:"restarts,omitempty"`
	NRestarts  int       `json:"n_restarts,omitempty"`

	HaveLast    bool    `json:"have_last"`
	LastT       int64   `json:"last_t"` // last good read (unix)
	LastElapsed int64   `json:"last_elapsed"`
	LastMH      float64 `json:"last_mh"`
	LastAttempt int64   `json:"last_attempt"` // last poll attempt, ok or not (unix)
}

type roundRun struct {
	roundState
	lastAt    time.Time // last good read (monotonic, this process only)
	attemptAt time.Time // last attempt (monotonic, this process only)
	seenTip   bool      // a good poll in this process already saw the current tip
	missedTip bool      // the tip changed while the miner wasn't answering
	noCounter bool      // the latest good read had no Total MH
}

// Rounds tracks the per-miner round counter, persisted as round-<id>.json.
type Rounds struct {
	dir string
	mu  sync.Mutex
	st  map[string]*roundRun
	// OnRestart is called (outside the lock) when a miner restart is detected.
	OnRestart func(id string, r Restart)
}

// RoundView is the round data included on /api/live.
type RoundView struct {
	Height     int64     `json:"height"`
	StartT     int64     `json:"start_t"`
	Counter    bool      `json:"counter"` // the miner reports Total MH
	Exact      bool      `json:"exact"`   // Hashes is the miner's own count for the whole round
	Hashes     string    `json:"hashes"`  // decimal integer (exceeds 2^53 on long rounds)
	AtMs       int64     `json:"at_ms"`   // when Hashes was read
	Partial    bool      `json:"partial"`
	PartialWhy string    `json:"partial_why,omitempty"`
	Restarts   []Restart `json:"restarts,omitempty"`
	NRestarts  int       `json:"n_restarts,omitempty"`
}

func NewRounds(dir string) *Rounds { return &Rounds{dir: dir, st: map[string]*roundRun{}} }

func (r *Rounds) file(id string) string { return filepath.Join(r.dir, "round-"+id+".json") }

func (r *Rounds) load(id string) *roundRun {
	if s := r.st[id]; s != nil {
		return s
	}
	s := &roundRun{}
	if r.dir != "" {
		if b, err := os.ReadFile(r.file(id)); err == nil {
			_ = json.Unmarshal(b, &s.roundState)
		}
	}
	r.st[id] = s
	return s
}

func (r *Rounds) saveLocked(id string, s *roundRun) {
	if r.dir == "" {
		return
	}
	b, err := json.Marshal(s.roundState)
	if err != nil {
		return
	}
	tmp := r.file(id) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, r.file(id))
	}
}

// Observe records one poll. tip is the latest network block height HashWatch
// knows (0 = unknown). It returns the restart detected by this poll, if any.
func (r *Rounds) Observe(id string, snap *model.Snapshot, tip int64, now time.Time) *Restart {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	s := r.load(id)
	unix := now.Unix()
	// Did HashWatch go a while without even trying to poll (it was closed, asleep or restarting)?
	gap := false
	if !s.attemptAt.IsZero() {
		gap = now.Sub(s.attemptAt) > RoundOfflineGap
	} else if s.LastAttempt > 0 {
		gap = unix-s.LastAttempt > int64(RoundOfflineGap/time.Second)
	} else if s.HaveLast {
		gap = true
	}
	s.attemptAt, s.LastAttempt = now, unix
	if gap {
		s.seenTip = false
	}

	if snap == nil || !snap.OK {
		if tip > 0 && s.Height > 0 && tip > s.Height {
			s.missedTip = true
		}
		r.mu.Unlock()
		return nil
	}
	if snap.Shares.TotalMH == nil {
		s.noCounter = true
		r.mu.Unlock()
		return nil
	}
	s.noCounter = false
	mh := *snap.Shares.TotalMH
	elapsed := snap.Uptime.Seconds

	var restart *Restart
	dirty := false
	if s.HaveLast {
		var dt int64
		if !s.lastAt.IsZero() {
			dt = int64(now.Sub(s.lastAt) / time.Second) // monotonic: immune to clock jumps
		} else {
			dt = unix - s.LastT
		}
		if dt < 0 {
			dt = 0
		}
		rebooted := mh < s.LastMH
		if elapsed > 0 && s.LastElapsed > 0 {
			rebooted = rebooted || elapsed < s.LastElapsed || (elapsed-s.LastElapsed) < dt-roundUptimeSlack
		}
		if rebooted {
			down := dt - elapsed
			if down < 0 || elapsed <= 0 {
				down = 0
			}
			rs := Restart{T: unix - elapsed, DownSec: down, Unknown: gap}
			if elapsed <= 0 {
				rs.T = unix
			}
			if s.Height > 0 {
				if c := s.LastMH - s.StartMH; c > 0 {
					s.CarryMH += c
				}
				s.StartMH = 0
				if gap && !s.Partial {
					s.Partial, s.PartialWhy = true, PartialOffline
				}
				s.Restarts = append(s.Restarts, rs)
				if len(s.Restarts) > 5 {
					s.Restarts = s.Restarts[len(s.Restarts)-5:]
				}
				s.NRestarts++
			}
			restart = &rs
			dirty = true
		}
	}

	if tip > 0 {
		if s.Height == 0 || tip > s.Height {
			live := s.seenTip && !gap && !s.missedTip && s.HaveLast
			why := ""
			if !live {
				why = PartialOffline
				if s.missedTip && !gap {
					why = PartialUnreachable
				}
			}
			s.Height, s.StartT, s.StartMH, s.CarryMH = tip, unix, mh, 0
			s.Partial, s.PartialWhy = !live, why
			s.Restarts, s.NRestarts = nil, 0
			dirty = true
		}
		s.seenTip, s.missedTip = true, false
	} else {
		s.seenTip = false
	}
	s.HaveLast, s.LastT, s.LastElapsed, s.LastMH, s.lastAt = true, unix, elapsed, mh, now
	if dirty {
		r.saveLocked(id, s)
	}
	cb := r.OnRestart
	r.mu.Unlock()
	if restart != nil && cb != nil {
		cb(id, *restart)
	}
	return restart
}

// View returns the current round for a miner (nil before the first round).
func (r *Rounds) View(id string) *RoundView {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.load(id)
	if s.Height == 0 && !s.noCounter {
		return nil
	}
	v := &RoundView{Height: s.Height, StartT: s.StartT, Counter: !s.noCounter, Partial: s.Partial, PartialWhy: s.PartialWhy,
		NRestarts: s.NRestarts, Restarts: append([]Restart(nil), s.Restarts...), AtMs: s.LastT * 1000}
	if !s.lastAt.IsZero() {
		v.AtMs = s.lastAt.UnixMilli()
	}
	c := s.CarryMH + s.LastMH - s.StartMH
	if !s.HaveLast || c < 0 {
		c = 0
	}
	v.Hashes = strconv.FormatInt(int64(math.Round(c*1e6)), 10)
	v.Exact = v.Counter && !s.Partial && s.Height > 0 && s.HaveLast
	return v
}

// Save writes every miner's round state.
func (r *Rounds) Save() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.st {
		r.saveLocked(id, s)
	}
}

// Remove forgets a miner's round.
func (r *Rounds) Remove(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.st, id)
	if r.dir != "" {
		_ = os.Remove(r.file(id))
	}
}
