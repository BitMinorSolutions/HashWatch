package app

import (
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
)

func TestMiningRecapSharesHighlightsAndMilestones(t *testing.T) {
	dir := t.TempDir()
	st := NewStats(dir)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
	st.Now = func() time.Time { return now }
	f64 := func(v float64) *float64 { return &v }
	snap := func(acc, best float64) *model.Snapshot {
		return &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs5m: f64(4)}, Shares: model.Shares{Accepted: f64(acc), BestShare: f64(best)},
			Uptime: model.Uptime{Seconds: 100}}
	}
	net := netdata.Snapshot{Difficulty: 1e14}

	st.Observe("m", "Nano", snap(100, 2e6))
	// first reading: no share delta, no highlight
	st.Observe("m", "Nano", snap(125, 2e6))
	v := st.View("m", net)
	if v.Today.Shares != 25 {
		t.Fatalf("shares today %d want 25", v.Today.Shares)
	}
	if len(v.Highlights) != 0 {
		t.Fatalf("first best should not highlight: %+v", v.Highlights)
	}
	var got1m bool
	for _, b := range v.Milestones {
		if b.ID == "share_1m" && b.Earned != 0 {
			got1m = true
		}
	}
	if !got1m {
		t.Fatalf("1M share milestone should be earned, got %+v", v.Milestones)
	}

	now = now.Add(5 * time.Second)
	st.Observe("m", "Nano", snap(130, 9e6))
	v = st.View("m", net)
	if len(v.Highlights) != 1 || v.Highlights[0].Kind != "best_ever" || v.Highlights[0].Value != 9e6 {
		t.Fatalf("new ever best highlight %+v", v.Highlights)
	}

	st.Save()
	st2 := NewStats(dir)
	st2.Now = st.Now
	v2 := st2.View("m", net)
	if v2.Today.Shares != 30 || len(v2.Highlights) != 1 || v2.Highlights[0].Value != 9e6 {
		t.Fatalf("recap did not persist: shares=%d highlights=%+v", v2.Today.Shares, v2.Highlights)
	}
}
