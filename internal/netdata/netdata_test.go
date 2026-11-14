package netdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
