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
