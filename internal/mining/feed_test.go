package mining

import "testing"

func TestFormatDiff(t *testing.T) {
	cases := map[float64]string{6400: "6.4K", 3997: "4.0K", 76205629: "76.2M", 12: "12", 0: "", -1: ""}
	for v, want := range cases {
		if got := FormatDiff(v); got != want {
			t.Errorf("FormatDiff(%v)=%q want %q", v, got, want)
		}
	}
}

func TestFeedLine(t *testing.T) {
	now := int64(1_000_000)
	got := FeedLine(Hit{T: now - 12, Diff: 6400, N: 1}, now)
	if got != "Share found · 6.4K · 12s ago" {
		t.Fatal(got)
	}
	got = FeedLine(Hit{T: now - 12, Diff: 6400, N: 3}, now)
	if got != "3 shares found · latest 6.4K · 12s ago" {
		t.Fatal(got)
	}
	got = FeedLine(Hit{T: now - 12, Diff: 0, N: 3}, now)
	if got != "3 shares found · 12s ago" {
		t.Fatal(got)
	}
	got = FeedLine(Hit{T: now - 2, Diff: 76205629, N: 1}, now)
	if got != "Share found · 76.2M · just now" {
		t.Fatal(got)
	}
}

func TestIsNewBest(t *testing.T) {
	if !IsNewBest(Hit{Diff: 76205629}, 76205629) {
		t.Fatal("exact")
	}
	if IsNewBest(Hit{Diff: 6400}, 76205629) {
		t.Fatal("pool share is not the best")
	}
	if IsNewBest(Hit{Diff: 0}, 76205629) {
		t.Fatal("must not invent a difficulty")
	}
}

func TestNewestFirst(t *testing.T) {
	var hits []Hit
	for i := 1; i <= 12; i++ {
		hits = append(hits, Hit{T: int64(i), Diff: float64(i * 1000), N: 1})
	}
	got := NewestFirst(hits, 8)
	if len(got) != 8 || got[0].T != 12 || got[7].T != 5 {
		t.Fatalf("%+v", got)
	}
	if NewestFirst(nil, 8) != nil {
		t.Fatal("empty")
	}
}
