package app

import (
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/netdata"
)

func TestNetViewStaleUsesReceiptTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	f := netdata.New(t.TempDir())
	f.Now = func() time.Time { return now }
	f.Set(netdata.Snapshot{
		Difficulty: 1,
		TipHeight:  100,
		NetFetched: now.Unix() - 30*60, // last receipt 30 min ago
		Blocks:     []netdata.Block{{Height: 100, Time: now.Unix() - 40*60, Diff: 1}},
	})
	s := &Server{Cfg: &Config{}, Net: f, Now: func() time.Time { return now }}
	_, info := s.netView()
	if stale, _ := info["network_stale"].(bool); !stale {
		t.Fatalf("expected stale when last receipt is 30 min old, got %#v", info)
	}

	// Same old tip block, but a successful stats receipt just now.
	f.Set(netdata.Snapshot{
		Difficulty: 1,
		TipHeight:  100,
		NetFetched: now.Unix(),
		Blocks:     []netdata.Block{{Height: 100, Time: now.Unix() - 40*60, Diff: 1}},
	})
	_, info = s.netView()
	if stale, _ := info["network_stale"].(bool); stale {
		t.Fatalf("must not be stale after a fresh receipt with a 40-minute-old tip: %#v", info)
	}
}
