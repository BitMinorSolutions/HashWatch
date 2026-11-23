package app

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/internal/tunnel"
)

const testPayoutAddr = "bc1qsimulatedminer000000000000000000000"

func TestShareLiveRedactsPayoutAddress(t *testing.T) {
	m := sim.New()
	m.Pools[0].User = testPayoutAddr + ".Nano3"
	m.Pools[1].User = testPayoutAddr
	m.Pools[2].User = testPayoutAddr + ".backup-worker-with-a-long-suffix-name"
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	host, portS, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portS)
	miner := Miner{ID: "sim1", Name: "sim", Host: host, Port: port, Kind: "cgminer"}

	s, home, path := shareServerWithMiner(t, miner)

	// wait for a snapshot
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snap := s.Poll.Latest("sim1"); snap != nil && snap.OK && len(snap.Pools) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := s.Poll.Latest("sim1")
	if snap == nil || !snap.OK {
		t.Fatal("no live snapshot")
	}
	if snap.Pools[0].User != testPayoutAddr+".Nano3" {
		t.Fatalf("sim user %q", snap.Pools[0].User)
	}

	far := &tclient{t: t, ts: home.ts, ip: "203.0.113.50"}
	if r, _ := far.do("GET", path, ""); r.StatusCode != 303 {
		t.Fatalf("share link: %d", r.StatusCode)
	}
	r, out := far.do("GET", "/api/live?miner=sim1", "")
	if r.StatusCode != 200 {
		t.Fatalf("view live: %d %v", r.StatusCode, out)
	}
	raw, _ := json.Marshal(out)
	body := string(raw)
	if strings.Contains(body, testPayoutAddr) {
		t.Fatalf("payout address leaked in share /api/live: %s", body)
	}
	data, _ := out["data"].(map[string]any)
	pools, _ := data["pools"].([]any)
	if len(pools) < 2 {
		t.Fatalf("pools %v", data["pools"])
	}
	u0, _ := pools[0].(map[string]any)["user"].(string)
	u1, _ := pools[1].(map[string]any)["user"].(string)
	u2, _ := pools[2].(map[string]any)["user"].(string)
	if u0 != "Nano3" || u1 != "Worker 2" || u2 != "backup-worker-with-a-long-suffix-name" {
		t.Fatalf("view pool users %q %q %q", u0, u1, u2)
	}

	// Admin /api/live may still include the address (needed to keep the current
	// worker when saving a pool). The dashboard must not render it.
	_, admin := home.do("GET", "/api/live?miner=sim1", "")
	araw, _ := json.Marshal(admin)
	if !strings.Contains(string(araw), testPayoutAddr) {
		t.Fatal("admin live should still carry the pool user for set-pool")
	}
}

func shareServerWithMiner(t *testing.T, miner Miner) (*Server, *tclient, string) {
	t.Helper()
	t.Setenv("HASHWATCH_CLOUDFLARED", fakeCloudflared(t))
	ts, s := newTestServer(t, []Miner{miner}, false, "")
	s.Tunnel = &tunnel.Manager{Dir: t.TempDir(), Origin: ts.URL, OnURL: s.TunnelURL}
	t.Cleanup(s.Tunnel.Stop)
	home := &tclient{t: t, ts: ts}
	if r, out := home.do("POST", "/api/remote", `{"enabled":true,"password":"secret1"}`); r.StatusCode != 200 {
		t.Fatalf("share on: %d %v", r.StatusCode, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Tunnel.Status().State != "online" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return s, home, "/v/" + s.Cfg.Security.ViewLink
}
