package cgminer

import (
	"math"
	"net"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

// MM ID0 string reconstructed from a live Avalon Nano 3 (fw 25103101) via its stock dashboard.
const nano3MM = `Ver[nano3-25103101_0736b2e] LVer[25103101_0736b2e] BVer[25103101_0736b2e] HVer[25103101_b906c52_1733f44] FW[Release] DNA[0000000000000000] ` +
	`HashStatus[1] PFCnt[32] NETFAIL[1794174334 1794174335 1794202627] Elapsed[549874] LW[13728938] MH[385] DHW[0] HW[385] DH[1.827%] ` +
	`Temp[41] OTemp[36] TMax[92] TAvg[90] TarT[90] Fan1[3150] FanR[43%] Vo[0] PS[0 0 0 4 2756 126 328] GHSspd[4164.65] DHspd[1.827%] ` +
	`GHSmm[4241.60] GHSavg[3227.08] WU[45081.76] Freq[0.00] Led[3] MGHS[3227.08] MTmax[92] MTavg[90] TA[10] Core[A3198S] PING[138] ` +
	`SoftOFF[0] ECHU[0] ECMM[0] PLL0[0 337 656 777] SF0[100 574 594 614] PVT_T0[88  91  91  89  88  88  92  91  91  87] ` +
	`PVT_V0[318 309 313 316 316 316 315 309 314 314] MW0[13874 13534 14249 13947 14149 13732 13750 13753 13782 13715] CRC[0] WORKLEVEL[2] MPO[125] ADJ[1]`

func fakeMiner(t *testing.T, replies map[string]string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 512)
				n, _ := c.Read(buf)
				req := string(buf[:n])
				for cmd, rep := range replies {
					if req == `{"command":"`+cmd+`"}` {
						c.Write([]byte(rep + "\x00"))
						return
					}
				}
				c.Write([]byte(`{"STATUS":[{"STATUS":"E","Msg":"Invalid command"}],"id":1}` + "\x00"))
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestFetchNano3(t *testing.T) {
	mmJSON := `"MM ID0":"` + nano3MM + `"`
	addr := fakeMiner(t, map[string]string{
		"summary": `{"STATUS":[{"STATUS":"S"}],"SUMMARY":[{"Elapsed":549866,"MHS av":3221871.73,"MHS 5s":3549241.48,"MHS 1m":3718275.11,"MHS 5m":3939073.37,"MHS 15m":3961218.50,` +
			`"Accepted":134043,"Rejected":233,"Hardware Errors":385,"Utility":14.63,"Stale":2627,"Get Failures":32,"Remote Failures":24,"Network Blocks":770,"Best Share":423759812,` +
			`"Device Hardware%":0.0001,"Pool Rejected%":0.1638,"Pool Stale%":0.5190,"Getworks":18886,"Found Blocks":0}],"id":1}`,
		"version": `{"STATUS":[{"STATUS":"S"}],"VERSION":[{"CGMiner":"4.11.1","API":"3.7","PROD":"Avalonnano","MODEL":"nano3","HWTYPE":"PMMv1_X1","SWTYPE":"MM318_X2","LVERSION":"25103101_0736b2e"}],"id":1}`,
		"pools": `{"STATUS":[{"STATUS":"S"}],"POOLS":[{"POOL":0,"URL":"stratum+tcp://solo.ckpool.org:3333","Status":"Alive","Priority":0,"Quota":1,"User":"bc1qexample.Nano3","Accepted":134043,"Rejected":233,"Stale":2627,` +
			`"Last Share Time":1794622151,"Stratum Active":true,"Stratum URL":"solo.ckpool.org","Best Share":423759812,"Pool Rejected%":0.1638,"Pool Stale%":0.5190,"Work Difficulty":3997.0,"Last Share Difficulty":3997.0,"Current Block Height":975731},` +
			`{"POOL":1,"URL":"stratum+tcp://umbrel.local:2018","Status":"Dead","Priority":1,"Stratum Active":false,"Accepted":0},]}`, // trailing comma quirk
		"devs":   `{"STATUS":[{"STATUS":"S"}],"DEVS":[{"ASC":0,"Name":"AVANANO","ID":0,"Enabled":"Y","Status":"Alive"}],"id":1}`,
		"estats": `{"STATUS":[{"STATUS":"S"}],"STATS":[{"STATS":0,"ID":"AVANANO0","Elapsed":549874,` + mmJSON + `}],"id":1}`,
	})
	s := Fetch(addr, 2*time.Second)
	if !s.OK || len(s.QueryErrors) > 0 {
		t.Fatalf("not ok: %v", s.QueryErrors)
	}
	eq := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || (*got-want) > 1e-6 || (want-*got) > 1e-6 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	eq("ths_5m", s.Hashrate.THs5m, 3.93907337)
	eq("ths_hero", s.Hashrate.THsHero, 4.16465) // GHSspd 4164.65
	eq("ghs_spd", s.Hashrate.GHsSpd, 4164.65)
	eq("temp max", s.Temps.Max, 92)
	eq("temp ambient", s.Temps.Ambient, 41)
	eq("outlet", s.Temps.Outlet, 36)
	eq("target", s.Temps.Target, 90)
	eq("fan rpm", s.Fan.RPM, 3150)
	eq("fan pct", s.Fan.Percent, 43)
	eq("watts", s.Power.Watts, 126)
	eq("volts", s.Power.VoltageV, 27.56)
	eq("mpo", s.Power.MPOW, 125)
	eq("dh", s.Shares.DHPct, 1.827)
	eq("ping", s.Errors.PingMS, 138)
	if s.Identity.Firmware != "25103101_0736b2e" || s.Identity.Chips != 10 || s.Identity.Core != "A3198S" || s.Identity.ID != "AVANANO0" {
		t.Errorf("identity %+v", s.Identity)
	}
	if len(s.Chips.Temps) != 10 || s.Chips.Temps[6] != 92 || len(s.Chips.VoltsMV) != 10 || len(s.Chips.Nonces) != 10 {
		t.Errorf("chips %+v", s.Chips)
	}
	if len(s.Pools) != 2 || !s.Pools[0].StratumActive || s.Pools[0].BlockHeight != 975731 || s.Pools[1].Status != "Dead" {
		t.Errorf("pools %+v", s.Pools)
	}
	if s.Pools[0].WorkDifficulty == nil || *s.Pools[0].WorkDifficulty != 3997 || s.Pools[0].LastShareDiff == nil || *s.Pools[0].LastShareDiff != 3997 {
		t.Errorf("pool diffs %+v", s.Pools[0])
	}
	if len(s.Errors.NetFail) != 3 || s.Uptime.Human != "6d 8h 44m" {
		t.Errorf("netfail/uptime %+v %+v", s.Errors.NetFail, s.Uptime)
	}
	if len(s.Warnings) != 0 {
		t.Errorf("Nano 3 fixture should stay green, got %v", s.Warnings)
	}
	if s.Control == nil || s.Control.WorkName != "high" {
		t.Errorf("control %+v", s.Control)
	}
}

func TestDecodeQuirks(t *testing.T) {
	r, err := decode([]byte(`{"STATUS":[{"STATUS":"S"}],"STATS":[{"A":1},]}{"id":1}` + "\x00"))
	if err != nil || r["STATS"] == nil {
		t.Fatalf("decode: %v %v", r, err)
	}
}

func TestRejectsWrites(t *testing.T) {
	if _, err := Query("127.0.0.1:1", "ascset", time.Second); err == nil {
		t.Fatal("write command must be refused")
	}
}

func TestHeroRate(t *testing.T) {
	spd, m1, m5 := 5023.0, 5.13, 4.51
	h := model.Hashrate{GHsSpd: &spd, THs1m: &m1, THs5m: &m5}
	got := heroRate(h)
	if got == nil || math.Abs(*got-5.023) > 1e-9 {
		t.Fatalf("prefer GHSspd: %v", deref(got))
	}
	h.GHsSpd = nil
	got = heroRate(h)
	if got == nil || *got != 5.13 {
		t.Fatalf("fallback MHS 1m: %v", deref(got))
	}
	h.THs1m = nil
	got = heroRate(h)
	if got == nil || *got != 4.51 {
		t.Fatalf("fallback MHS 5m: %v", deref(got))
	}
	// GHSspd present as zero is still the live chip reading, not a reason to invent an average.
	z := 0.0
	h = model.Hashrate{GHsSpd: &z, THs1m: &m1, THs5m: &m5}
	got = heroRate(h)
	if got == nil || *got != 0 {
		t.Fatalf("zero GHSspd: %v", deref(got))
	}
}
