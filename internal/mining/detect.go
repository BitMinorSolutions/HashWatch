// Package mining detects accepted shares and network block-height changes
// from the data HashWatch already polls. It never invents share difficulties.
package mining

import (
	"strings"

	"github.com/hashwatch/hashwatch/internal/model"
)

// Sample is the miner data used to detect a new share or a new block height.
type Sample struct {
	OK            bool
	Accepted      float64
	HasAccepted   bool
	LastShareUnix int64
	LastShareDiff float64
	HasLastDiff   bool
	WorkDiff      float64
	HasWorkDiff   bool
	PoolID        int
	HasPool       bool
	Height        int64
	HasHeight     bool
	Uptime        int64
	BestShare     float64
	PoolURL       string
	PoolName      string
}

// Hit is one chart point: the latest accepted share seen in a poll, plus how
// many accepted shares that poll covered. N>1 means other shares landed too,
// but only this latest difficulty is known.
type Hit struct {
	T    int64   `json:"t"`
	Diff float64 `json:"diff"`
	N    int     `json:"n"`
}

// HeightState is the block the miner is working on, and when HashWatch first
// saw this height (only after it observed a change).
type HeightState struct {
	Height    int64  `json:"height,omitempty"`
	HasHeight bool   `json:"has_height"`
	ChangedAt int64  `json:"changed_at,omitempty"`
	Pool      string `json:"pool,omitempty"`
	PoolURL   string `json:"pool_url,omitempty"`
	Changed   bool   `json:"-"`
}

// FromSnapshot reads detection inputs from a poll. Difficulty for a hit is
// Last Share Difficulty only (never filled in from Work Difficulty).
func FromSnapshot(s *model.Snapshot) Sample {
	if s == nil || !s.OK {
		return Sample{}
	}
	out := Sample{OK: true, Uptime: s.Uptime.Seconds}
	if s.Shares.Accepted != nil {
		out.Accepted, out.HasAccepted = *s.Shares.Accepted, true
	}
	if s.Shares.BestShare != nil {
		out.BestShare = *s.Shares.BestShare
	}
	p := activePool(s.Pools)
	if p != nil {
		out.HasPool, out.PoolID = true, p.ID
		out.LastShareUnix = p.LastShareUnix
		out.PoolURL, out.PoolName = p.URL, shortPool(p)
		if p.LastShareDiff != nil && *p.LastShareDiff > 0 {
			out.LastShareDiff, out.HasLastDiff = *p.LastShareDiff, true
		}
		if p.WorkDifficulty != nil && *p.WorkDifficulty > 0 {
			out.WorkDiff, out.HasWorkDiff = *p.WorkDifficulty, true
		}
		if p.BlockHeight > 0 {
			out.Height, out.HasHeight = p.BlockHeight, true
		}
	}
	return out
}

func activePool(pools []model.Pool) *model.Pool {
	for i := range pools {
		if pools[i].StratumActive {
			return &pools[i]
		}
	}
	if len(pools) > 0 {
		return &pools[0]
	}
	return nil
}

func shortPool(p *model.Pool) string {
	u := p.StratumURL
	if u == "" {
		u = p.URL
	}
	for _, pre := range []string{"stratum+tcp://", "stratum+ssl://", "stratum+tls://"} {
		u = strings.TrimPrefix(u, pre)
	}
	if i := strings.LastIndex(u, ":"); i > 0 {
		return u[:i]
	}
	return u
}

// DetectShare compares two polls. A reboot (Accepted or Elapsed went down)
// resets the baseline and returns no hit. A pool switch does not reset the
// global Accepted counter. Multiple shares between polls become one hit with
// N>1 and only the latest last-share difficulty.
func DetectShare(prev *Sample, cur Sample, now int64) *Hit {
	if !cur.OK || !cur.HasAccepted {
		return nil
	}
	if prev == nil || !prev.OK || !prev.HasAccepted {
		return nil
	}
	if reboot(prev, cur) {
		return nil
	}
	delta := cur.Accepted - prev.Accepted
	if delta < 1 {
		return nil
	}
	n := int(delta)
	// A fractional Accepted would be unexpected; round down already via int.
	if n < 1 {
		return nil
	}
	hit := &Hit{N: n, T: now}
	if cur.LastShareUnix > 0 {
		// Prefer the pool's last-share timestamp when it moved forward (or is
		// at least as new as the previous one). Never invent a time in the future.
		if cur.LastShareUnix >= prev.LastShareUnix {
			hit.T = cur.LastShareUnix
		}
	}
	if cur.HasLastDiff {
		hit.Diff = cur.LastShareDiff
	}
	// If we don't know the latest difficulty, still record the count so the
	// UI can say shares arrived, but Diff stays 0 (the chart skips those).
	return hit
}

func reboot(prev *Sample, cur Sample) bool {
	if cur.HasAccepted && prev.HasAccepted && cur.Accepted < prev.Accepted {
		return true
	}
	if cur.Uptime > 0 && prev.Uptime > 0 && cur.Uptime < prev.Uptime {
		return true
	}
	return false
}

// DetectHeight returns the current height state. ChangedAt is only set when
// HashWatch has witnessed a height change (including across a restart, if the
// persisted clock and height still match).
func DetectHeight(prev *Sample, cur Sample, prevChangedAt, prevHeight, now int64) HeightState {
	st := HeightState{}
	if !cur.OK || !cur.HasHeight {
		return st
	}
	st.HasHeight, st.Height = true, cur.Height
	st.Pool, st.PoolURL = cur.PoolName, cur.PoolURL
	if prev == nil || !prev.OK || !prev.HasHeight {
		if prevHeight > 0 && cur.Height == prevHeight {
			st.ChangedAt = prevChangedAt
			return st
		}
		if prevHeight > 0 && cur.Height != prevHeight {
			st.ChangedAt = now
			st.Changed = true
			return st
		}
		return st
	}
	if cur.Height != prev.Height {
		st.ChangedAt = now
		st.Changed = true
		return st
	}
	st.ChangedAt = prevChangedAt
	return st
}
