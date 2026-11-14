package netdata

import (
	"context"
	"strings"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"
)

const blocksJSON = `[{"id":"a","height":975765,"timestamp":1794641661,"tx_count":7730,"difficulty":134823837323272.7,"extras":{"reward":312696824,"totalFees":196824,"pool":{"id":36,"name":"F2Pool","slug":"f2pool"}}},
{"id":"b","height":975764,"timestamp":1794641639,"tx_count":10,"difficulty":134823837323272.7,"extras":{"reward":312503176,"totalFees":3176,"pool":{"id":1,"name":"Solo CK","slug":"solock"}}},
{"id":"c","height":975763,"timestamp":1794641555,"tx_count":10,"difficulty":134823837323272.7,"extras":{"reward":312500000,"totalFees":0,"pool":{"id":0,"name":"Unknown","slug":"unknown"}}}]`

func TestRefreshAndCache(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "down", 503)
			return
		}
		switch r.URL.Path {
		case "/api/v1/blocks":
			w.Write([]byte(blocksJSON))
		case "/api/v1/prices":
			w.Write([]byte(`{"time":1,"USD":67017}`))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	f := New(dir)
	f.Base = srv.URL + "/api"
	if err := f.RefreshNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.RefreshPrice(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := f.Get()
	if s.TipHeight != 975765 || s.Difficulty != 134823837323272.7 || s.PriceUSD != 67017 || s.AvgFeesSat != (196824+3176)/3 {
		t.Fatalf("%+v", s)
	}
	if !s.Blocks[1].Solo || s.Blocks[0].Solo || !s.Blocks[2].Unknown {
		t.Fatalf("classify: %+v", s.Blocks)
	}
	// offline: the cached values survive, with the error recorded
	fail = true
	if f.RefreshNetwork(context.Background()) == nil {
		t.Fatal("want error")
	}
	if s2 := f.Get(); s2.Difficulty != s.Difficulty || s2.LastErr == "" {
		t.Fatalf("cache lost: %+v", s2)
	}
	// and a fresh process reads them back from disk
	if g := New(dir).Get(); g.TipHeight != 975765 || g.PriceUSD != 67017 {
		t.Fatalf("disk cache: %+v", g)
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		name, slug    string
		solo, unknown bool
	}{{"Solo CK", "solock", true, false}, {"Public Pool", "publicpool", true, false}, {"Foundry USA", "foundryusa", false, false},
		{"Unknown", "unknown", false, true}, {"", "", false, true}, {"Consolidated", "x", false, false}} {
		s, u := Classify(c.name, c.slug)
		if s != c.solo || u != c.unknown {
			t.Errorf("%q: got solo=%v unknown=%v", c.name, s, u)
		}
	}
}

func TestDefaultIntervals(t *testing.T) {
	f := New(t.TempDir())
	if f.Every != 30*time.Second || f.PriceEvery != 60*time.Second {
		t.Fatalf("defaults: every=%v price=%v", f.Every, f.PriceEvery)
	}
}

func TestNextBackoff(t *testing.T) {
	// First error doubles the default interval; later errors double again, capped at 5 min.
	if g := nextBackoff(30*time.Second, MaxBackoff); g != 60*time.Second {
		t.Fatalf("first net: %v", g)
	}
	if g := nextBackoff(60*time.Second, MaxBackoff); g != 120*time.Second {
		t.Fatalf("second: %v", g)
	}
	if g := nextBackoff(60*time.Second, MaxBackoff); g != 2*time.Minute {
		t.Fatalf("price first: %v", g)
	}
	if g := nextBackoff(4*time.Minute, MaxBackoff); g != MaxBackoff {
		t.Fatalf("cap: %v", g)
	}
	if g := nextBackoff(MaxBackoff, MaxBackoff); g != MaxBackoff {
		t.Fatalf("already at cap: %v", g)
	}
}

func TestRefresh429KeepsLastGood(t *testing.T) {
	fail := false
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if fail {
			http.Error(w, "slow down", 429)
			return
		}
		switch r.URL.Path {
		case "/api/v1/blocks":
			w.Write([]byte(blocksJSON))
		case "/api/v1/prices":
			w.Write([]byte(`{"time":1,"USD":67017}`))
		}
	}))
	defer srv.Close()
	f := New(t.TempDir())
	f.Base = srv.URL + "/api"
	if err := f.RefreshNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.RefreshPrice(context.Background()); err != nil {
		t.Fatal(err)
	}
	good := f.Get()
	if good.NetFetched == 0 || good.PriceFetched == 0 {
		t.Fatalf("missing timestamps: %+v", good)
	}
	fail = true
	if err := f.RefreshNetwork(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("want HTTP 429, got %v", err)
	}
	if err := f.RefreshPrice(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("want HTTP 429, got %v", err)
	}
	s := f.Get()
	if s.Difficulty != good.Difficulty || s.PriceUSD != good.PriceUSD || s.TipHeight != good.TipHeight {
		t.Fatalf("last good values lost: %+v", s)
	}
	if s.NetFetched != good.NetFetched || s.PriceFetched != good.PriceFetched {
		t.Fatalf("timestamps overwritten on error: net %d->%d price %d->%d", good.NetFetched, s.NetFetched, good.PriceFetched, s.PriceFetched)
	}
	if s.LastErr == "" || s.PriceErr == "" {
		t.Fatalf("errors not recorded: %+v", s)
	}
}

func TestTimeUntil(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	// Both due immediately when never fetched.
	if d := timeUntil(now, true, time.Time{}, 30*time.Second, true, time.Time{}, 60*time.Second); d != 0 {
		t.Fatalf("initial: %v", d)
	}
	lastNet := now.Add(-10 * time.Second)
	lastPrice := now.Add(-10 * time.Second)
	if d := timeUntil(now, true, lastNet, 30*time.Second, true, lastPrice, 60*time.Second); d != 20*time.Second {
		t.Fatalf("remaining: %v", d)
	}
	// Network backoff of 2 min (90s left) is sooner than price (170s left).
	if d := timeUntil(now, true, now.Add(-30*time.Second), 2*time.Minute, true, now.Add(-10*time.Second), 3*time.Minute); d != 90*time.Second {
		t.Fatalf("backoff remaining: %v", d)
	}
	if d := timeUntil(now, false, time.Time{}, 30*time.Second, false, time.Time{}, 60*time.Second); d != 20*time.Second {
		t.Fatalf("both off: %v", d)
	}
}
