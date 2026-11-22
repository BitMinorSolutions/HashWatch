package mining

import (
	"testing"

	"github.com/hashwatch/hashwatch/internal/model"
)

func f64(v float64) *float64 { return &v }

func TestDetectShareSingleAndBurst(t *testing.T) {
	prev := Sample{OK: true, HasAccepted: true, Accepted: 100, LastShareUnix: 1000, HasLastDiff: true, LastShareDiff: 4000, PoolID: 0, HasPool: true, Uptime: 5000}
	// one new share
	cur := prev
	cur.Accepted = 101
	cur.LastShareUnix = 1005
	cur.LastShareDiff = 8120
	hit := DetectShare(&prev, cur, 1010)
	if hit == nil || hit.N != 1 || hit.Diff != 8120 || hit.T != 1005 {
		t.Fatalf("single: %+v", hit)
	}
	// several shares between polls: latest + count, do not invent the others
	cur.Accepted = 104 // 3 more after the previous cur? from prev 100 -> 104 is 4
	cur.LastShareUnix = 1008
	cur.LastShareDiff = 15000
	hit = DetectShare(&prev, cur, 1010)
	if hit == nil || hit.N != 4 || hit.Diff != 15000 || hit.T != 1008 {
		t.Fatalf("burst: %+v", hit)
	}
}

func TestDetectShareNoInventOnUnchangedAccepted(t *testing.T) {
	prev := Sample{OK: true, HasAccepted: true, Accepted: 50, LastShareUnix: 10, HasLastDiff: true, LastShareDiff: 4000, Uptime: 100}
	cur := prev
	cur.LastShareUnix = 20 // time moved but Accepted did not — not an accepted share
	if hit := DetectShare(&prev, cur, 30); hit != nil {
		t.Fatalf("unexpected hit %+v", hit)
	}
}

func TestDetectShareMinerReboot(t *testing.T) {
	prev := Sample{OK: true, HasAccepted: true, Accepted: 9000, LastShareUnix: 500, HasLastDiff: true, LastShareDiff: 4000, Uptime: 80000}
	cur := Sample{OK: true, HasAccepted: true, Accepted: 3, LastShareUnix: 10, HasLastDiff: true, LastShareDiff: 4000, Uptime: 12}
	if hit := DetectShare(&prev, cur, 20); hit != nil {
		t.Fatalf("reboot should reset, not count a drop as shares: %+v", hit)
	}
	// first post-reboot poll is the new baseline
	if hit := DetectShare(nil, cur, 20); hit != nil {
		t.Fatalf("no prev should not invent: %+v", hit)
	}
	next := cur
	next.Accepted = 4
	next.LastShareUnix = 18
	next.LastShareDiff = 6200
	hit := DetectShare(&cur, next, 22)
	if hit == nil || hit.N != 1 || hit.Diff != 6200 {
		t.Fatalf("share after reboot baseline: %+v", hit)
	}
}

func TestDetectSharePoolSwitchUsesGlobalAccepted(t *testing.T) {
	prev := Sample{OK: true, HasAccepted: true, Accepted: 200, LastShareUnix: 50, HasLastDiff: true, LastShareDiff: 3997, PoolID: 0, HasPool: true, Uptime: 1000}
	// switch to pool 1; global accepted rose by 2; latest last-share is on the new pool
	cur := Sample{OK: true, HasAccepted: true, Accepted: 202, LastShareUnix: 60, HasLastDiff: true, LastShareDiff: 1024, PoolID: 1, HasPool: true, Uptime: 1008}
	hit := DetectShare(&prev, cur, 70)
	if hit == nil || hit.N != 2 || hit.Diff != 1024 || hit.T != 60 {
		t.Fatalf("pool switch: %+v", hit)
	}
	// switching pools with no new accepted shares is not a hit (and not a reboot)
	idle := prev
	idle.PoolID = 1
	idle.LastShareUnix = 0
	idle.HasLastDiff = false
	if hit := DetectShare(&prev, idle, 70); hit != nil {
		t.Fatalf("idle pool switch: %+v", hit)
	}
}

func TestDetectHeightTimer(t *testing.T) {
	prev := Sample{OK: true, HasHeight: true, Height: 977080, PoolName: "solo.ckpool.org"}
	cur := prev
	st := DetectHeight(&prev, cur, 0, 977080, 1000)
	if !st.HasHeight || st.Height != 977080 || st.ChangedAt != 0 || st.Changed {
		t.Fatalf("unchanged first watch: %+v", st)
	}
	cur.Height = 977081
	st = DetectHeight(&prev, cur, 0, 977080, 1500)
	if !st.Changed || st.ChangedAt != 1500 || st.Height != 977081 {
		t.Fatalf("height change: %+v", st)
	}
	// later poll, same height: keep the clock
	st = DetectHeight(&cur, cur, 1500, 977081, 1800)
	if st.Changed || st.ChangedAt != 1500 || st.Height != 977081 {
		t.Fatalf("hold clock: %+v", st)
	}
	// first observation of a height: no clock (we didn't see the network find it)
	st = DetectHeight(nil, prev, 0, 0, 1000)
	if st.ChangedAt != 0 || st.Changed || st.Height != 977080 {
		t.Fatalf("first look: %+v", st)
	}
	// restart: no in-memory prev, but the persisted clock for this same height is kept
	st = DetectHeight(nil, cur, 1500, 977081, 2000)
	if st.Changed || st.ChangedAt != 1500 || st.Height != 977081 {
		t.Fatalf("restart same height: %+v", st)
	}
	st = DetectHeight(nil, cur, 1500, 977080, 2000)
	if !st.Changed || st.ChangedAt != 2000 || st.Height != 977081 {
		t.Fatalf("restart and height moved: %+v", st)
	}
}

func TestFromSnapshotLastShareNotWorkDiff(t *testing.T) {
	work, last := 3997.0, 88000.0
	acc := 10.0
	s := &model.Snapshot{OK: true, Uptime: model.Uptime{Seconds: 99}, Shares: model.Shares{Accepted: &acc, BestShare: &last},
		Pools: []model.Pool{{ID: 0, URL: "stratum+tcp://solo.ckpool.org:3333", StratumActive: true,
			WorkDifficulty: &work, LastShareDiff: &last, Difficulty: &last, LastShareUnix: 42, BlockHeight: 977086}}}
	got := FromSnapshot(s)
	if !got.HasLastDiff || got.LastShareDiff != 88000 || !got.HasWorkDiff || got.WorkDiff != 3997 || got.Height != 977086 || got.PoolName != "solo.ckpool.org" {
		t.Fatalf("%+v", got)
	}
}
