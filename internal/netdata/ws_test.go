package netdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestApplyWSMessageBlockAndPrice(t *testing.T) {
	f := New(t.TempDir())
	now := time.Unix(1_700_000_000, 0)
	f.Now = func() time.Time { return now }

	blockJSON := `{
		"block": {
			"height": 977109,
			"timestamp": 1699999999,
			"tx_count": 3000,
			"difficulty": 134823837323272.7,
			"extras": {"reward": 312500000, "totalFees": 8000000, "pool": {"name": "Solo CK", "slug": "solock"}}
		},
		"conversions": {"time": 1700000000000, "USD": 97321.5, "EUR": 90000},
		"da": {"progressPercent": 32.5, "difficultyChange": 0.78, "estimatedRetargetDate": 1735514828279, "remainingBlocks": 1361, "nextRetargetHeight": 876960, "previousRetarget": 4.4}
	}`
	if err := f.applyWSMessage([]byte(blockJSON)); err != nil {
		t.Fatal(err)
	}
	s := f.Get()
	if s.TipHeight != 977109 {
		t.Fatalf("tip %d", s.TipHeight)
	}
	if s.Difficulty < 1e14 {
		t.Fatalf("diff %v", s.Difficulty)
	}
	if s.PriceUSD != 97321.5 {
		t.Fatalf("price %v", s.PriceUSD)
	}
	if s.PriceSource != "mempool.space" {
		t.Fatalf("price source %q", s.PriceSource)
	}
	if !s.WSConnected || s.WSLastEvent != now.Unix() {
		t.Fatalf("ws flags %+v", s)
	}
	if len(s.Blocks) != 1 || !s.Blocks[0].Solo || s.Blocks[0].Pool != "Solo CK" {
		t.Fatalf("blocks %+v", s.Blocks)
	}
	if s.DiffAdj == nil || s.DiffAdj.RemainingBlocks != 1361 || s.DiffAdj.EstimatedRetargetDate != 1735514828 {
		t.Fatalf("da %+v", s.DiffAdj)
	}
	if s.Rev < 1 {
		t.Fatalf("rev %d", s.Rev)
	}

	// A later block is prepended and becomes the tip.
	if err := f.applyWSMessage([]byte(`{"block":{"height":977110,"timestamp":1700000600,"difficulty":134823837323272.7,"extras":{}}}`)); err != nil {
		t.Fatal(err)
	}
	s = f.Get()
	if s.TipHeight != 977110 || len(s.Blocks) != 2 || s.Blocks[0].Height != 977110 {
		t.Fatalf("merge tip %d blocks %+v", s.TipHeight, s.Blocks)
	}
}

func TestApplyWSMessageBlocksArray(t *testing.T) {
	f := New(t.TempDir())
	msg := `{"blocks":[
		{"height":10,"timestamp":100,"difficulty":2,"extras":{}},
		{"height":9,"timestamp":90,"difficulty":2,"extras":{}}
	]}`
	if err := f.applyWSMessage([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	s := f.Get()
	if s.TipHeight != 10 || len(s.Blocks) != 2 {
		t.Fatalf("got tip %d n=%d", s.TipHeight, len(s.Blocks))
	}
}

func TestWaitWakesOnChange(t *testing.T) {
	f := New(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan Snapshot, 1)
	go func() {
		s, _ := f.Wait(ctx, 0)
		done <- s
	}()
	time.Sleep(30 * time.Millisecond)
	_ = f.applyWSMessage([]byte(`{"conversions":{"USD":100000}}`))
	select {
	case s := <-done:
		if s.PriceUSD != 100000 {
			t.Fatalf("price %v", s.PriceUSD)
		}
	case <-ctx.Done():
		t.Fatal("Wait did not wake")
	}
}

func TestWSSessionLiveUpdate(t *testing.T) {
	// A tiny websocket server that accepts, ignores want/init, then pushes a price.
	pushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		// read client hello (init + want) then push
		go func() {
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
			}
		}()
		time.Sleep(20 * time.Millisecond)
		body, _ := json.Marshal(map[string]any{"conversions": map[string]any{"USD": 88888.0}, "block": map[string]any{"height": 42, "timestamp": 1, "difficulty": 3.5, "extras": map[string]any{}}})
		if err := c.Write(ctx, websocket.MessageText, body); err != nil {
			t.Errorf("write: %v", err)
			return
		}
		close(pushed)
		<-ctx.Done()
	}))
	defer srv.Close()

	f := New(t.TempDir())
	f.WSURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go f.Run(ctx)

	select {
	case <-pushed:
	case <-ctx.Done():
		t.Fatal("server never pushed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := f.Get()
		if s.PriceUSD == 88888 && s.TipHeight == 42 && s.WSConnected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := f.Get()
	t.Fatalf("did not apply live update: %+v", s)
}

func TestRESTFallbackWhenWSDisabled(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.URL.Path, "prices") {
			w.Write([]byte(`{"USD":12345}`))
			return
		}
		w.Write([]byte(`[{"height":7,"timestamp":1,"difficulty":9,"extras":{}}]`))
	}))
	defer srv.Close()

	f := New(t.TempDir())
	f.Base = srv.URL
	f.PriceURL = srv.URL + "/v1/prices"
	f.WSURL = "-" // force REST
	f.Every = 50 * time.Millisecond
	f.PriceEvery = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	go f.Run(ctx)
	<-ctx.Done()
	s := f.Get()
	if s.TipHeight != 7 || s.PriceUSD != 12345 {
		t.Fatalf("REST fallback did not fill snapshot: %+v hits=%d", s, hits)
	}
	if s.WSConnected {
		t.Fatal("ws should be off")
	}
	if hits < 2 {
		t.Fatalf("expected REST hits, got %d", hits)
	}
}

func TestApplyWSMessageDoesNotBumpOnSameValues(t *testing.T) {
	f := New(t.TempDir())
	f.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	block := []byte(`{"block":{"height":800000,"timestamp":1700000000,"tx_count":1,"difficulty":42,"extras":{"reward":1,"totalFees":1,"pool":{"name":"P","slug":"p"}}}}`)
	price := []byte(`{"conversions":{"USD":100.5}}`)
	if err := f.applyWSMessage(block); err != nil {
		t.Fatal(err)
	}
	if err := f.applyWSMessage(price); err != nil {
		t.Fatal(err)
	}
	s1 := f.Get()
	if s1.Rev < 1 || s1.PriceUSD != 100.5 || s1.TipHeight != 800000 {
		t.Fatalf("setup snapshot: %+v", s1)
	}
	if err := f.applyWSMessage(block); err != nil {
		t.Fatal(err)
	}
	if err := f.applyWSMessage(price); err != nil {
		t.Fatal(err)
	}
	s2 := f.Get()
	if s2.Rev != s1.Rev {
		t.Fatalf("rev bumped on identical payloads: %d -> %d", s1.Rev, s2.Rev)
	}
	if err := f.applyWSMessage([]byte(`{"conversions":{"USD":101}}`)); err != nil {
		t.Fatal(err)
	}
	s3 := f.Get()
	if s3.Rev <= s2.Rev || s3.PriceUSD != 101 {
		t.Fatalf("price change should bump rev and update: %+v", s3)
	}
}

func TestNetworkFreshnessIsReceiptNotBlockAge(t *testing.T) {
	f := New(t.TempDir())
	start := time.Unix(1_700_000_000, 0)
	now := start
	f.Now = func() time.Time { return now }

	block := []byte(`{"block":{"height":800000,"timestamp":1699990000,"difficulty":42,"extras":{"reward":1,"totalFees":1,"pool":{"name":"P","slug":"p"}}},"da":{"progressPercent":10,"difficultyChange":0.5,"remainingBlocks":1000,"nextRetargetHeight":810000,"previousRetarget":1}}`)
	if err := f.applyWSMessage(block); err != nil {
		t.Fatal(err)
	}
	s0 := f.Get()
	if s0.NetFetched != start.Unix() {
		t.Fatalf("initial net_fetched %d", s0.NetFetched)
	}
	if s0.NetworkStale(start.Unix()) {
		t.Fatal("fresh receipt should not be stale")
	}

	// 30 minutes of difficulty-adjustment ticks, no new block.
	da := []byte(`{"da":{"progressPercent":10,"difficultyChange":0.5,"remainingBlocks":1000,"nextRetargetHeight":810000,"previousRetarget":1}}`)
	for i := 0; i < 15; i++ {
		now = now.Add(2 * time.Minute)
		if err := f.applyWSMessage(da); err != nil {
			t.Fatal(err)
		}
		s := f.Get()
		if s.TipHeight != 800000 {
			t.Fatalf("tip moved: %d", s.TipHeight)
		}
		if s.NetFetched != now.Unix() {
			t.Fatalf("da tick should refresh net_fetched, got %d want %d", s.NetFetched, now.Unix())
		}
		if s.NetworkStale(now.Unix()) {
			t.Fatalf("stale at +%s while WS stats kept arriving (block age %s)", now.Sub(start), now.Sub(time.Unix(1699990000, 0)))
		}
		if s.Rev != s0.Rev {
			t.Fatalf("identical da should not bump rev: %d -> %d", s0.Rev, s.Rev)
		}
	}
	if now.Sub(start) < 30*time.Minute {
		t.Fatalf("clock only advanced %s", now.Sub(start))
	}
}

func TestRESTKeepsNetworkFreshWhenWSQuiet(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.URL.Path, "prices") {
			w.Write([]byte(`{"USD":111}`))
			return
		}
		w.Write([]byte(`[{"height":9,"timestamp":1,"difficulty":4,"extras":{}}]`))
	}))
	defer srv.Close()

	f := New(t.TempDir())
	start := time.Unix(1_700_000_000, 0)
	now := start
	f.Now = func() time.Time { return now }
	f.Base = srv.URL
	f.PriceURL = srv.URL + "/v1/prices"
	f.WSURL = "-"

	if err := f.RefreshNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.RefreshPrice(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Same tip for 30 minutes; REST still succeeds and refreshes receipt time.
	now = now.Add(30 * time.Minute)
	if err := f.RefreshNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := f.Get()
	if s.TipHeight != 9 || s.NetFetched != now.Unix() {
		t.Fatalf("REST should refresh receipt: %+v", s)
	}
	if s.NetworkStale(now.Unix()) {
		t.Fatal("not stale after 30 min of successful REST with no new block")
	}
}

func TestNetworkStaleWhenAllSourcesFail(t *testing.T) {
	var fail bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "down", 500)
			return
		}
		if strings.Contains(r.URL.Path, "prices") {
			w.Write([]byte(`{"USD":111}`))
			return
		}
		w.Write([]byte(`[{"height":9,"timestamp":1,"difficulty":4,"extras":{}}]`))
	}))
	defer srv.Close()

	f := New(t.TempDir())
	start := time.Unix(1_700_000_000, 0)
	now := start
	f.Now = func() time.Time { return now }
	f.Base = srv.URL
	f.PriceURL = srv.URL + "/v1/prices"
	f.DiffFallback = "http://127.0.0.1:1/nope"
	f.PriceFallback = "http://127.0.0.1:1/nope"
	f.WSURL = "-"

	if err := f.RefreshNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	good := f.Get().NetFetched
	fail = true
	now = now.Add(15 * time.Minute)
	if err := f.RefreshNetwork(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	s := f.Get()
	if s.NetFetched != good {
		t.Fatalf("failed fetch overwrote net_fetched %d -> %d", good, s.NetFetched)
	}
	if !s.NetworkStale(now.Unix()) {
		t.Fatal("should be stale after 15 min with every source failing")
	}
	if s.PriceStale(now.Unix()) {
		// price was fetched at start and not refreshed; 15 > 10 so price is also stale — that's fine
	}
}

func TestRESTPollsWhenWebsocketIsQuiet(t *testing.T) {
	var hits int
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.URL.Path, "prices") {
			w.Write([]byte(`{"USD":77}`))
			return
		}
		w.Write([]byte(`[{"height":3,"timestamp":1,"difficulty":2,"extras":{}}]`))
	}))
	defer rest.Close()

	// Websocket accepts and stays up but never sends application data.
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer ws.Close()

	f := New(t.TempDir())
	f.Base = rest.URL
	f.PriceURL = rest.URL + "/v1/prices"
	f.WSURL = "ws" + strings.TrimPrefix(ws.URL, "http")
	f.Every = 40 * time.Millisecond
	f.PriceEvery = 40 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	go f.Run(ctx)
	<-ctx.Done()
	s := f.Get()
	if hits < 2 {
		t.Fatalf("REST should keep polling while the socket is quiet, hits=%d ws=%v snap=%+v", hits, s.WSConnected, s)
	}
	if s.TipHeight != 3 || s.PriceUSD != 77 {
		t.Fatalf("REST should fill snapshot while WS is quiet: %+v hits=%d", s, hits)
	}
	if s.NetworkStale(f.Now().Unix()) {
		t.Fatal("quiet-WS REST path should not leave network stale")
	}
}
