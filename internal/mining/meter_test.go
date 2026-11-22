package mining

import (
	"math"
	"testing"
)

func TestLogPos(t *testing.T) {
	if LogPos(0, 1e14) != 0 || LogPos(1, 1e14) != 0 || LogPos(1e14, 1e14) != 1 {
		t.Fatal("edges")
	}
	if LogPos(2e14, 1e14) != 1 {
		t.Fatal("above max")
	}
	// Midpoint on a log scale from 1 to 1e6 is 1e3.
	if got := LogPos(1e3, 1e6); math.Abs(got-0.5) > 1e-12 {
		t.Fatal(got)
	}
	if LogPos(4000, 0) != 0 || LogPos(-1, 1e14) != 0 {
		t.Fatal("unknown")
	}
}

func realistic() ([]Marker, float64) {
	// User's miner on 22 Nov 2026: pool ~4K, best ever 76,205,629 (also today's
	// best), network difficulty ~134.8 T.
	net := 134.8238373232727e12
	return []Marker{
		{ID: "pool", Label: "Pool share needed", Short: "Pool share", Value: 3997},
		{ID: "today", Label: "Your best today", Short: "Best today", Value: 76205629},
		{ID: "ever", Label: "Your best ever", Short: "Best ever", Value: 76205629},
		{ID: "win", Label: "Winning a block", Short: "Winning a block", Value: net},
	}, net
}

func TestPlaceOmitsUnknownAndHidesWinWithoutNet(t *testing.T) {
	got := Place([]Marker{
		{ID: "pool", Label: "Pool share needed", Short: "Pool share", Value: 4000},
		{ID: "today", Label: "Your best today", Short: "Best today", Value: 0},
		{ID: "ever", Label: "Your best ever", Short: "Best ever", Value: 76205629},
		{ID: "win", Label: "Winning a block", Value: 0},
	}, 0, 390)
	if len(got) != 0 {
		t.Fatalf("without network difficulty want no markers, got %+v", got)
	}
}

func TestPlaceStaggersSamePositionAndFitsPhone(t *testing.T) {
	ms, net := realistic()
	for _, width := range []float64{320, 390, 1100} {
		got := Place(ms, net, width)
		if len(got) != 4 {
			t.Fatalf("width %v: got %d markers", width, len(got))
		}
		var today, ever, pool, win *Placed
		for i := range got {
			p := &got[i]
			switch p.ID {
			case "today":
				today = p
			case "ever":
				ever = p
			case "pool":
				pool = p
			case "win":
				win = p
			}
		}
		if today == nil || ever == nil || pool == nil || win == nil {
			t.Fatalf("width %v missing marker: %+v", width, got)
		}
		if today.Side == ever.Side {
			t.Fatalf("width %v: today and ever are both %s, labels would overlap", width, today.Side)
		}
		if !nearX(today.X, ever.X) {
			t.Fatalf("width %v: today and ever should share an x, got %v and %v", width, today.X, ever.X)
		}
		if win.X != 1 || win.Align != "end" || win.Side != "above" {
			t.Fatalf("width %v: win placement %+v", width, win)
		}
		if width < NarrowPx {
			if today.Label != "Best today" || ever.Label != "Best ever" || pool.Label != "Pool share" {
				t.Fatalf("width %v should use short labels: %+v", width, got)
			}
		} else {
			if today.Label != "Your best today" || ever.Label != "Your best ever" {
				t.Fatalf("width %v should use full labels: %+v", width, got)
			}
		}
		if err := overlapError(got, width); err != "" {
			t.Fatalf("width %v: %s (%+v)", width, err, got)
		}
		// Every label stays inside the bar width.
		for _, p := range got {
			l, r := span(p, width)
			if l < -0.5 || r > width+0.5 {
				t.Fatalf("width %v: %s spans [%v, %v] outside 0..%v", width, p.Label, l, r, width)
			}
		}
	}
}

func TestPlaceSpreadsDistinctMarkers(t *testing.T) {
	net := 134.8238373232727e12
	ms := []Marker{
		{ID: "pool", Label: "Pool share needed", Short: "Pool share", Value: 3997},
		{ID: "today", Label: "Your best today", Short: "Best today", Value: 2e6},
		{ID: "ever", Label: "Your best ever", Short: "Best ever", Value: 76205629},
		{ID: "win", Label: "Winning a block", Short: "Winning a block", Value: net},
	}
	for _, width := range []float64{320, 390, 1100} {
		got := Place(ms, net, width)
		if err := overlapError(got, width); err != "" {
			t.Fatalf("width %v: %s (%+v)", width, err, got)
		}
		// pool should sit left of today, today left of ever, ever left of win.
		idx := map[string]float64{}
		for _, p := range got {
			idx[p.ID] = p.X
		}
		if !(idx["pool"] < idx["today"] && idx["today"] < idx["ever"] && idx["ever"] < idx["win"]) {
			t.Fatalf("order %+v", idx)
		}
	}
}

func TestCloseText(t *testing.T) {
	if CloseText(0, 1e14, "1%") != "" || CloseText(1, 0, "1%") != "" || CloseText(1, 1, "") != "" {
		t.Fatal("unknown inputs must yield no sentence")
	}
	got := CloseText(76205629, 1e14, "0.000076%")
	want := "Your best share is 0.000076% of the way to winning a block."
	if got != want {
		t.Fatalf("%q", got)
	}
}

func overlapError(got []Placed, width float64) string {
	for i := 0; i < len(got); i++ {
		for j := i + 1; j < len(got); j++ {
			if overlaps(got[i], got[j], width) {
				return got[i].Label + " overlaps " + got[j].Label
			}
		}
	}
	return ""
}
