package cgminer

import (
	"testing"
	"time"
)

func TestProbeDedupsNano3SName(t *testing.T) {
	addr := fakeMiner(t, map[string]string{
		"version": nano3sVersion,
	})
	desc, ok := Probe(addr, 2*time.Second)
	if !ok {
		t.Fatal("probe failed")
	}
	if desc != "Avalon Nano3s" {
		t.Fatalf("Probe name %q, want %q (not doubled)", desc, "Avalon Nano3s")
	}
}
