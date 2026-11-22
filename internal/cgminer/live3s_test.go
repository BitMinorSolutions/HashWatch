package cgminer

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/hashwatch/hashwatch/internal/model"
)

// liveReplies loads testdata/nano3s-live.txt: the raw API replies captured
// from the owner's hashing Avalon Nano 3S on 2026-11-22 ("== cmd" + JSON).
func liveReplies(t *testing.T) map[string]map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/nano3s-live.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	cmd := ""
	text := strings.TrimPrefix(string(b), "\ufeff") // the capture is saved with a BOM and CRLF
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "== "):
			cmd = strings.TrimPrefix(ln, "== ")
		case ln != "" && cmd != "":
			var m map[string]any
			if err := json.Unmarshal([]byte(ln), &m); err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
			out[cmd] = m
		}
	}
	return out
}

// Every value the dashboard shows, checked against the live 3S replies.
func TestLiveNano3SAccuracy(t *testing.T) {
	r := liveReplies(t)
	for _, statsCmd := range []string{"estats", "stats"} {
		s := &model.Snapshot{}
		Build(s, r["summary"], r["version"], r["pools"], r["devs"], r[statsCmd])
		eq := func(name string, got *float64, want float64) {
			t.Helper()
			if got == nil || math.Abs(*got-want) > 1e-9*math.Max(1, math.Abs(want)) {
				t.Errorf("%s: %s = %v, want %v", statsCmd, name, deref(got), want)
			}
		}
		// hashrate: MHS -> TH/s
		eq("5s", s.Hashrate.THs5s, 6.12826292)
		eq("1m", s.Hashrate.THs1m, 5.03655557)
		eq("5m", s.Hashrate.THs5m, 5.14583419)
		eq("15m", s.Hashrate.THs15m, 5.00919056)
		eq("avg", s.Hashrate.THsAvg, 5.12030053)
		eq("hero = GHSspd", s.Hashrate.THsHero, 5.01443)
		eq("GHSspd", s.Hashrate.GHsSpd, 5014.43)
		eq("GHSmm", s.Hashrate.GHsMM, 5154.36)
		eq("WU", s.Hashrate.WU, map[string]float64{"estats": 71863.72, "stats": 71863.24}[statsCmd])
		// shares
		eq("accepted", s.Shares.Accepted, 775)
		eq("rejected", s.Shares.Rejected, 5)
		eq("stale", s.Shares.Stale, 0)
		eq("reject %", s.Shares.RejectPct, 0.5546)
		eq("stale %", s.Shares.StalePct, 0)
		eq("hw errors", s.Shares.HWErrors, 0)
		eq("Device Hardware%", s.Shares.DeviceHWPct, 0)
		eq("DH", s.Shares.DHPct, 2.542)
		eq("best share", s.Shares.BestShare, 6988470)
		eq("found blocks", s.Shares.FoundBlocks, 0)
		eq("network blocks", s.Shares.NetworkBlocks, 5)
		eq("get failures", s.Shares.GetFailures, 0)
		eq("remote failures", s.Shares.RemoteFailures, 0)
		// power: PS[0 0 27482 3 0 3444 94]
		eq("watts", s.Power.Watts, 94)
		eq("volts", s.Power.VoltageV, 27.482)
		eq("MPO", s.Power.MPOW, 95)
		if s.Power.PSOK == nil || !*s.Power.PSOK {
			t.Errorf("ps ok")
		}
		// work speed: WORKMODE[1] (Medium, as set in the Avalon app), WORKLEVEL[0]
		if s.Power.WorkLevel == nil || *s.Power.WorkLevel != 1 || s.Control == nil || s.Control.WorkName != "medium" || *s.Control.WorkLevel != 1 {
			t.Errorf("work level %v %+v", s.Power.WorkLevel, s.Control)
		}
		// temps: TMax 92, TAvg 84, TarT 85, OTemp 65, ITemp -273 (no sensor) -> missing
		eq("max", s.Temps.Max, 92)
		eq("avg", s.Temps.Avg, 84)
		eq("target", s.Temps.Target, 85)
		eq("outlet", s.Temps.Outlet, 65)
		if s.Temps.Ambient != nil {
			t.Errorf("board temp should be missing, got %v", *s.Temps.Ambient)
		}
		wantT := []float64{77, 89, 92, 90, 92, 88, 77, 76, 87, 87, 85, 76}
		wantV := []float64{283, 267, 271, 269, 270, 270, 288, 282, 273, 274, 281, 283}
		wantN := []float64{84, 82, 71, 73, 77, 79, 69, 97, 95, 74, 81, 77}
		for i := range wantT {
			if len(s.Chips.Temps) != 12 || s.Chips.Temps[i] != wantT[i] || s.Chips.VoltsMV[i] != wantV[i] || s.Chips.Nonces[i] != wantN[i] {
				t.Fatalf("chips %+v", s.Chips)
			}
		}
		// fan
		eq("fan rpm", s.Fan.RPM, 1120)
		eq("fan %", s.Fan.Percent, 26)
		// uptime: summary Elapsed 3197 s
		if s.Uptime.Seconds != 3197 || s.Uptime.Human != "53m" {
			t.Errorf("uptime %+v", s.Uptime)
		}
		// light: LEDUser[1-100-50-0-0-255] = steady, 100 %, blue
		if s.Control.LEDMode != "fixed" || *s.Control.LEDBright != 100 || s.Control.LEDHex != "#0000ff" || *s.Control.LEDTemper != 50 {
			t.Errorf("light %+v", s.Control)
		}
		// errors / misc
		eq("ping", s.Errors.PingMS, 122)
		eq("ECMM", s.Errors.ECMM, 0)
		eq("CRC", s.Errors.CRC, 0)
		eq("soft off", s.Errors.SoftOff, 0)
		if len(s.Errors.NetFail) != 0 || s.Errors.HashStatus != nil {
			t.Errorf("errors %+v", s.Errors)
		}
		// identity
		id := s.Identity
		if id.Product != "Avalon Nano3s" || id.Model != "Nano3s" || id.Firmware != "25061101_97e23a6" || id.Core != "A3197S" || id.Chips != 12 ||
			id.CGMiner != "4.11.1" || id.API != "3.7" || id.HWType != "N_MM1v1_X1" || id.SWType != "MM319" || id.MAC != "40a5ef7113c5" || id.ID != "AVALON0" {
			t.Errorf("identity %+v", id)
		}
		if s.Device.Name != "AVALON" || s.Device.Status != "Alive" || s.Device.Enabled != "Y" {
			t.Errorf("device %+v", s.Device)
		}
		// pools
		if len(s.Pools) != 3 {
			t.Fatalf("pools %d", len(s.Pools))
		}
		p0, p1, p2 := s.Pools[0], s.Pools[1], s.Pools[2]
		if p0.URL != "stratum+tcp://solo.ckpool.org:3333" || p0.User != "bc1qexamplepayoutaddressredactedxxxxxxxxx" || p0.Status != "Alive" || p0.Priority != 0 ||
			!p0.StratumActive || p0.LastShareUnix != 1795372495 || p0.BlockHeight != 977086 || p0.StratumURL != "solo.ckpool.org" {
			t.Errorf("pool0 %+v", p0)
		}
		eq("pool0 accepted", p0.Accepted, 775)
		eq("pool0 rejected", p0.Rejected, 5)
		eq("pool0 difficulty", p0.Difficulty, 5685)
		eq("pool0 best", p0.BestShare, 6988470)
		eq("pool0 reject%", p0.RejectPct, 0.5546)
		if p1.URL != "stratum+tcp://eusolo.ckpool.org:3333" || p1.Priority != 1 || p1.StratumActive || p1.LastShareUnix != 0 || p1.Status != "Alive" {
			t.Errorf("pool1 %+v", p1)
		}
		if p2.URL != "stratum+tcp://public-pool.io:21496" || p2.Priority != 2 || p2.StratumActive || p2.BlockHeight != 977082 {
			t.Errorf("pool2 %+v", p2)
		}
		eq("pool1 difficulty (no work yet)", p1.Difficulty, 0)
		eq("pool2 difficulty (work diff)", p2.Difficulty, 100000)
		// live 3S (TAvg 84, TarT 85, TMax 92, DH 2.542%) must stay green
		if len(s.Warnings) != 0 {
			t.Errorf("%s: live 3S should stay green, got %v", statsCmd, s.Warnings)
		}
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
