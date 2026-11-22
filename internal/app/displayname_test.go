package app

import (
	"testing"

	"github.com/hashwatch/hashwatch/internal/model"
)

func TestDisplayMinerNameDedupsDoubledDefault(t *testing.T) {
	snap := &model.Snapshot{Identity: model.Identity{Product: "Avalon Nano3s", Model: "Nano3s"}}
	m := Miner{Name: "Avalon Nano3s Nano3s"}
	if got := displayMinerName(m, snap); got != "Avalon Nano3s" {
		t.Fatalf("got %q", got)
	}
	// a name the user actually chose is left alone
	m.Name = "Basement miner"
	if got := displayMinerName(m, snap); got != "Basement miner" {
		t.Fatalf("custom name got %q", got)
	}
	// no snapshot yet: keep the stored name
	if got := displayMinerName(Miner{Name: "Avalon Nano3s Nano3s"}, nil); got != "Avalon Nano3s Nano3s" {
		t.Fatalf("no snap got %q", got)
	}
}
