package mining

import "testing"

func TestTimeWindowFitsDataWithMinimum(t *testing.T) {
	now := int64(1_000_000)
	// ten minutes of hits: axis is still 45 minutes, ending at now
	hits := []Hit{{T: now - 600, Diff: 6400, N: 1}, {T: now - 10, Diff: 6400, N: 1}}
	t0, t1 := TimeWindow(hits, now, 0, 0)
	if t1 != now || now-t0 != MinWindow {
		t.Fatalf("short history window [%d, %d] span %d, want t1=now span %d", t0, t1, t1-t0, MinWindow)
	}
	// two hours of hits: axis fits the data (plus a small left pad)
	hits = []Hit{{T: now - 7200, Diff: 6400, N: 1}, {T: now - 10, Diff: 8000, N: 1}}
	t0, t1 = TimeWindow(hits, now, 0, 0)
	if t1 != now || t0 != (now-7200)-30 {
		t.Fatalf("2h history window [%d, %d]", t0, t1)
	}
	// 30 hours of hits: cap at 24 h
	hits = []Hit{{T: now - 30*3600, Diff: 6400, N: 1}, {T: now - 10, Diff: 6400, N: 1}}
	t0, t1 = TimeWindow(hits, now, 0, 0)
	if t1 != now || t1-t0 != MaxWindow {
		t.Fatalf("long history span %d, want %d", t1-t0, MaxWindow)
	}
	t0, t1 = TimeWindow(nil, now, 0, 0)
	if t1 != now || t1-t0 != MinWindow {
		t.Fatalf("empty window [%d, %d]", t0, t1)
	}
}

func TestBucketHitsMergesPoolSharesKeepsLuckyShare(t *testing.T) {
	// 12 shares around 6.4K in one minute, plus a harder share in the same minute
	base := int64(1_000_020) // start of a 60s bucket
	var hits []Hit
	for i := 0; i < 12; i++ {
		hits = append(hits, Hit{T: base + int64(i*4), Diff: 6400, N: 1})
	}
	hits = append(hits, Hit{T: base + 20, Diff: 88000, N: 1})
	got := BucketHits(hits, 60)
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2 (pool pile + lucky share): %+v", len(got), got)
	}
	var pile, lucky *Hit
	for i := range got {
		if got[i].Diff == 88000 {
			lucky = &got[i]
		} else {
			pile = &got[i]
		}
	}
	if pile == nil || pile.N != 12 || pile.Diff != 6400 {
		t.Fatalf("pool pile %+v", pile)
	}
	if lucky == nil || lucky.N != 1 || lucky.Diff != 88000 {
		t.Fatalf("lucky %+v", lucky)
	}
}

func TestShareAxisKeepsNetworkSeparate(t *testing.T) {
	hits := []Hit{{Diff: 6400, N: 1, T: 1}, {Diff: 12000, N: 1, T: 2}}
	ymin, ymax, sep := ShareAxis(6400, 6_988_470, 134_823_837_323_272.7, hits)
	if !sep {
		t.Fatalf("network should be a top marker, not on the share axis (ymin=%g ymax=%g)", ymin, ymax)
	}
	if ymax < 6_988_470 || ymax > 6_988_470*4 {
		t.Fatalf("share ymax %g should sit just above the 7M best, not at 135T", ymax)
	}
	if ymin > 6400 {
		t.Fatalf("ymin %g hides pool shares", ymin)
	}
	// when the "best" is already on the network scale, keep one axis
	_, ymax2, sep2 := ShareAxis(6400, 1e14, 1.35e14, hits)
	if sep2 || ymax2 < 1.35e14 {
		t.Fatalf("near-network best should share the axis: sep=%v ymax=%g", sep2, ymax2)
	}
}

func TestReportedBestIgnoresHitDiffs(t *testing.T) {
	// A last-share point of 76M must not override the miner's 6,988,470 Best Share
	// (or HashWatch's stored/leaderboard best).
	if b := ReportedBest(6_988_470, 6_988_470, 6_988_470); b != 6_988_470 {
		t.Fatalf("got %g", b)
	}
	if b := ReportedBest(6_988_470, 8e6, 6_988_470); b != 8e6 {
		t.Fatalf("stored best should win: %g", b)
	}
}
