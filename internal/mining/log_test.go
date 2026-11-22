package mining

import (
	"testing"

	"github.com/hashwatch/hashwatch/internal/model"
)

func TestLogPersistAndFirstPollIsBaseline(t *testing.T) {
	dir := t.TempDir()
	l := NewLog(dir)
	acc, last, work := 100.0, 8000.0, 3997.0
	s1 := &model.Snapshot{OK: true, Uptime: model.Uptime{Seconds: 1000}, Shares: model.Shares{Accepted: &acc, BestShare: &last},
		Pools: []model.Pool{{ID: 0, URL: "stratum+tcp://solo.ckpool.org:3333", StratumActive: true,
			LastShareDiff: &last, WorkDifficulty: &work, LastShareUnix: 50, BlockHeight: 977080}}}
	if hit := l.Observe("m1", s1, 1000); hit != nil {
		t.Fatalf("first poll must be a baseline, got %+v", hit)
	}
	acc2, last2 := 103.0, 12000.0
	s2 := *s1
	s2.Shares.Accepted, s2.Shares.BestShare = &acc2, &last2
	s2.Pools = []model.Pool{{ID: 0, URL: "stratum+tcp://solo.ckpool.org:3333", StratumActive: true,
		LastShareDiff: &last2, WorkDifficulty: &work, LastShareUnix: 80, BlockHeight: 977081}}
	hit := l.Observe("m1", &s2, 1100)
	if hit == nil || hit.N != 3 || hit.Diff != 12000 || hit.T != 80 {
		t.Fatalf("burst hit: %+v", hit)
	}
	v := l.View("m1", 0)
	if !v.Height.Changed || v.Height.Height != 977081 || v.Height.ChangedAt != 1100 {
		t.Fatalf("height: %+v", v.Height)
	}
	if v.WorkDiff != 3997 || v.BestShare != 12000 || len(v.Hits) != 1 {
		t.Fatalf("view %+v", v)
	}
	l.Save()

	l2 := NewLog(dir)
	v2 := l2.View("m1", 0)
	if len(v2.Hits) != 1 || v2.Hits[0].N != 3 || v2.Height.ChangedAt != 1100 || v2.WorkDiff != 3997 {
		t.Fatalf("reloaded %+v", v2)
	}
	// after reload, first observe is a new baseline (prev not persisted — honest, no invented hit)
	acc3 := 104.0
	s3 := s2
	s3.Shares.Accepted = &acc3
	s3.Pools[0].LastShareUnix = 90
	if hit := l2.Observe("m1", &s3, 1200); hit != nil {
		t.Fatalf("first poll after reload must not invent a hit: %+v", hit)
	}
	if hs := l2.View("m1", 0).Height; hs.ChangedAt != 1100 || hs.Height != 977081 {
		t.Fatalf("restart must keep the block-height clock: %+v", hs)
	}
	acc4 := 105.0
	s4 := s3
	s4.Shares.Accepted = &acc4
	s4.Pools[0].LastShareDiff = &last2
	s4.Pools[0].LastShareUnix = 95
	hit = l2.Observe("m1", &s4, 1300)
	if hit == nil || hit.N != 1 {
		t.Fatalf("share after reload baseline: %+v", hit)
	}
	if n := len(l2.View("m1", 0).Hits); n != 2 { // persisted burst + new
		t.Fatalf("hits after reload observe: %d", n)
	}
}
