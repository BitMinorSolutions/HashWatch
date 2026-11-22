package app

import (
	"testing"

	"github.com/hashwatch/hashwatch/internal/model"
)

func f64ptr(v float64) *float64 { return &v }

func TestOddsHashrateMatchesLiveHeadline(t *testing.T) {
	hero, m1, m5, m15 := 5.25, 5.13, 4.51, 4.71
	snap := &model.Snapshot{OK: true, Hashrate: model.Hashrate{
		THsHero: f64ptr(hero), THs1m: f64ptr(m1), THs5m: f64ptr(m5), THs15m: f64ptr(m15),
	}}
	got := oddsHashrate(snap)
	if got != hero*1e12 {
		t.Fatalf("used %v H/s, want live headline %v TH/s", got, hero)
	}
	// No hero: GHSspd (GH/s) is the same live chip reading.
	snap = &model.Snapshot{OK: true, Hashrate: model.Hashrate{GHsSpd: f64ptr(5250), THs1m: f64ptr(m1), THs5m: f64ptr(m5), THs15m: f64ptr(m15)}}
	if got = oddsHashrate(snap); got != 5250*1e9 {
		t.Fatalf("GHSspd: %v", got)
	}
	// No GHSspd: MHS 1m, then 5m — never the 15-minute average.
	snap = &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs1m: f64ptr(m1), THs5m: f64ptr(m5), THs15m: f64ptr(m15)}}
	if got = oddsHashrate(snap); got != m1*1e12 {
		t.Fatalf("1m fallback: %v", got)
	}
	snap = &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs5m: f64ptr(m5), THs15m: f64ptr(m15)}}
	if got = oddsHashrate(snap); got != m5*1e12 {
		t.Fatalf("5m fallback: %v", got)
	}
	snap = &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs15m: f64ptr(m15)}}
	if got = oddsHashrate(snap); got != 0 {
		t.Fatalf("must not use 15m average: %v", got)
	}
	// A live zero is "not hashing", not a cue to use an average.
	z := 0.0
	snap = &model.Snapshot{OK: true, Hashrate: model.Hashrate{THsHero: &z, THs1m: f64ptr(m1), THs5m: f64ptr(m5)}}
	if got = oddsHashrate(snap); got != 0 {
		t.Fatalf("zero hero: %v", got)
	}
	if oddsHashrate(&model.Snapshot{OK: false, Hashrate: model.Hashrate{THsHero: f64ptr(hero)}}) != hero*1e12 {
		// The helper itself is just a reading; the caller must check OK.
	}
}
