package app

import (
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

func TestPollBackoff(t *testing.T) {
	p := &Poller{Every: DefaultPoll}
	want := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}
	for fails, w := range want {
		if got := p.interval(fails); got != w {
			t.Fatalf("fails=%d: got %v want %v", fails, got, w)
		}
	}
	if (&Poller{Every: time.Minute}).interval(1) != time.Minute {
		t.Fatal("backoff must never be faster than the normal interval")
	}
}

func TestThinHistory(t *testing.T) {
	now := int64(200000)
	var h []model.Sample
	for ts := now - 30*3600; ts <= now; ts += 5 {
		h = append(h, model.Sample{T: ts})
	}
	h = thin(h, now)
	if h[0].T < now-historyKeep {
		t.Fatal("kept samples older than 24h")
	}
	recent, old := 0, 0
	for i, x := range h {
		if x.T >= now-fullRes {
			recent++
		} else {
			old++
			if i > 0 && h[i-1].T < now-fullRes && x.T-h[i-1].T < thinEvery {
				t.Fatal("old samples closer than thinEvery")
			}
		}
	}
	if recent != fullRes/5+1 || old > (historyKeep-fullRes)/thinEvery+1 || len(h) > historyMax {
		t.Fatalf("recent=%d old=%d total=%d", recent, old, len(h))
	}
}

func TestThinAveragesLiveHashrate(t *testing.T) {
	now := int64(200000)
	f := func(v float64) *float64 { return &v }
	var h []model.Sample
	// Two hours back, 5s cadence: three live readings in the first old bucket.
	start := now - fullRes - 30
	for i, v := range []float64{3, 6, 9} {
		h = append(h, model.Sample{T: start + int64(i)*5, HHero: f(v), H5m: f(4)})
	}
	// And a recent full-res point that must be left alone.
	h = append(h, model.Sample{T: now - 10, HHero: f(8), H5m: f(5)})
	out := thin(h, now)
	if len(out) != 2 {
		t.Fatalf("want 2 samples, got %d", len(out))
	}
	if out[0].HHero == nil || *out[0].HHero != 6 {
		t.Fatalf("old live bucket should average 3,6,9 got %v", out[0].HHero)
	}
	if out[1].HHero == nil || *out[1].HHero != 8 {
		t.Fatalf("recent point should be unchanged, got %v", out[1].HHero)
	}
}
