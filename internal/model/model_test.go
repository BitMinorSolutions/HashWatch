package model

import "testing"

func TestDisplayModel(t *testing.T) {
	eq := func(p, m, want string) {
		t.Helper()
		if got := DisplayModel(p, m); got != want {
			t.Fatalf("DisplayModel(%q, %q) = %q, want %q", p, m, got, want)
		}
	}
	eq("Avalon Nano3s", "Nano3s", "Avalon Nano3s")
	eq("Avalon Nano3s", "nano3s", "Avalon Nano3s")
	eq("Avalonnano", "nano3", "Avalonnano nano3")
	eq("Avalon Nano 3", "", "Avalon Nano 3")
	eq("", "nano3", "nano3")
	eq("", "", "")
}

func TestLiveTHsFallback(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	if v := (Hashrate{THsHero: f(4.2), THs5m: f(3)}).LiveTHs(); v == nil || *v != 4.2 {
		t.Fatalf("hero %v", v)
	}
	if v := (Hashrate{GHsSpd: f(4164.65), THs5m: f(3)}).LiveTHs(); v == nil || *v != 4.16465 {
		t.Fatalf("ghs_spd %v", v)
	}
	z := 0.0
	if v := (Hashrate{GHsSpd: &z, THs5m: f(3)}).LiveTHs(); v == nil || *v != 0 {
		t.Fatalf("zero spd should not fall back, got %v", v)
	}
	if v := (Hashrate{THs1m: f(3.5), THs5m: f(3)}).LiveTHs(); v == nil || *v != 3.5 {
		t.Fatalf("1m %v", v)
	}
	if v := (Hashrate{THs5m: f(3)}).LiveTHs(); v == nil || *v != 3 {
		t.Fatalf("5m %v", v)
	}
	s := Snapshot{FetchedAt: 10, Hashrate: Hashrate{GHsSpd: f(5000)}}
	if s.Sample().HHero == nil || *s.Sample().HHero != 5 {
		t.Fatalf("sample live %v", s.Sample().HHero)
	}
}
