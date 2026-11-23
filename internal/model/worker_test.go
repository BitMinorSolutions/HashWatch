package model

import "testing"

func TestWorkerLabel(t *testing.T) {
	cases := []struct {
		user string
		idx  int
		want string
	}{
		{"bc1qsimulatedminer000000000000000000000.Nano3", 1, "Nano3"},
		{"bc1qsimulatedminer000000000000000000000", 1, "Worker 1"},
		{"bc1qsimulatedminer000000000000000000000", 2, "Worker 2"},
		{"bc1qsimulatedminer000000000000000000000.backup-worker", 3, "backup-worker"},
		{"", 1, "Worker 1"},
		{".Nano3", 1, "Nano3"},
		{"bc1qsimulatedminer000000000000000000000.", 1, "Worker 1"},
		{"  tb1qexampleaddressthatsfake00000000000.worker-1  ", 1, "worker-1"},
	}
	for _, c := range cases {
		if got := WorkerLabel(c.user, c.idx); got != c.want {
			t.Errorf("WorkerLabel(%q, %d) = %q, want %q", c.user, c.idx, got, c.want)
		}
		if c.want != "Nano3" && c.want != "backup-worker" && c.want != "worker-1" {
			if got := WorkerLabel(c.user, c.idx); containsAddr(got) {
				t.Errorf("label leaked address: %q", got)
			}
		}
	}
}

func containsAddr(s string) bool {
	return len(s) >= 20 && (hasPrefix(s, "bc1") || hasPrefix(s, "tb1") || hasPrefix(s, "1") || hasPrefix(s, "3"))
}
func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func TestRedactSnapshotForView(t *testing.T) {
	addr := "bc1qsimulatedminer000000000000000000000"
	src := &Snapshot{
		OK: true,
		Pools: []Pool{
			{User: addr + ".Nano3", URL: "stratum+tcp://solo.ckpool.org:3333"},
			{User: addr, URL: "stratum+tcp://solo.ckpool.org:3333"},
		},
		MMFields:    map[string]string{"note": "user " + addr + ".Nano3"},
		QueryErrors: []string{"fail for " + addr},
	}
	got := RedactSnapshotForView(src)
	if src.Pools[1].User != addr {
		t.Fatal("src was mutated")
	}
	if got.Pools[0].User != "Nano3" || got.Pools[1].User != "Worker 2" {
		t.Fatalf("pools %q %q", got.Pools[0].User, got.Pools[1].User)
	}
	if containsAddr(got.MMFields["note"]) || containsAddr(got.QueryErrors[0]) {
		t.Fatalf("mm/err leaked: %+v %v", got.MMFields, got.QueryErrors)
	}
	if got.MMFields["note"] != "user Nano3" {
		t.Fatalf("mm rewrite %q", got.MMFields["note"])
	}
}
