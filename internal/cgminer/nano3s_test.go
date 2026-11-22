package cgminer

import (
	"github.com/hashwatch/hashwatch/internal/model"
	"testing"
	"time"
)

// Verbatim from a real Avalon Nano 3S (fw 25061101), no pool work yet.
const nano3sMM = `Ver[Nano3s-25061101_97e23a6] LVer[25061101_97e23a6] BVer[25061101_97e23a6] FW[Release] LED[0-1] LEDUser[1-100-50-0-0-255] LcdOnoff[1] LcdSwitch[1] Volume[100] DNA[0201000099bd7915] MEMFREE[65320] PFCnt[0] NETFAIL[0 0 0 0 0 0 0 0] SYSTEMSTATU[Work: In Work, Hash Board: 1] Elapsed[6057] BOOTBY[0x03.00000000] LW[194991] MH[0] DHW[0] HW[0] DH[2.525%] ITemp[-273] OTemp[64] TMax[93] TAvg[85] TarT[85] Fan1[1140] FanR[26%] PS[0 0 27482 3 0 3444 94] GHSspd[5016.36] DHspd[2.525%] GHSmm[5153.95] GHSavg[0.00] WU[0.00] Freq[353.20] MGHS[0.00] Activation[1] MTmax[92] MTavg[85] TA[12] Core[A3197S] BIN[49] PING[0] SoftOFF[0] ECHU[0] ECMM[0] PLL0[837 549 306 132] SF0[336 357 378 396] PVT_T0[ 78  87  91  91  92  89  76  76  88  86  85  76] PVT_V0[284 266 271 269 270 269 290 283 273 272 280 283] MW0[0 0 0 0 0 0 0 0 0 0 0 0] CRC[0] COMCRC[0] ATA1[95-85-3470-338-20] WORKMODE[1] WORKLEVEL[0] MPO[95] CALIALL[7] ADJ[1]`

func nano3sReplies(version string) map[string]string {
	r := map[string]string{
		"summary": `{"STATUS":[{"STATUS":"S","When":1795314554,"Code":11,"Msg":"Summary","Description":"cgminer 4.11.1"}],"SUMMARY":[{"Elapsed":6056,"MHS av":0.00,"MHS 5s":0.00,"MHS 1m":0.00,"MHS 5m":0.00,"MHS 15m":0.00,"Found Blocks":0,"Getworks":0,"Accepted":0,"Rejected":0,"Hardware Errors":0,"Utility":0.00,"Discarded":0,"Stale":0,"Get Failures":0,"Local Work":0,"Remote Failures":0,"Network Blocks":0,"Total MH":0.0000,"Work Utility":0.00,"Difficulty Accepted":0.00000000,"Difficulty Rejected":0.00000000,"Difficulty Stale":0.00000000,"Best Share":0,"Device Hardware%":0.0000,"Device Rejected%":0.0000,"Pool Rejected%":0.0000,"Pool Stale%":0.0000,"Last getwork":0}],"id":1}`,
		"estats":  `{"STATUS":[{"STATUS":"S","When":1795314554,"Code":70,"Msg":"CGMiner stats","Description":"cgminer 4.11.1"}],"STATS":[{"STATS":0,"ID":"AVALON0","Elapsed":6056,"Calls":0,"Wait":0.000000,"Max":0.000000,"Min":99999999.000000,"MM ID0":"` + nano3sMM + `","MM Count":1,"Nonce Mask":25}],"id":1}`,
		"devs":    `{"STATUS":[{"STATUS":"S","When":1795314554,"Code":9,"Msg":"1 ASC(s)","Description":"cgminer 4.11.1"}],"DEVS":[{"ASC":0,"Name":"AVALON","ID":0,"Enabled":"Y","Status":"Alive","Temperature":0.00,"MHS av":0.00,"MHS 5s":0.00,"MHS 1m":0.00,"MHS 5m":0.00,"MHS 15m":0.00,"Accepted":0,"Rejected":0,"Hardware Errors":0,"Utility":0.00,"Last Share Pool":-1,"Last Share Time":0,"Total MH":0.0000,"Diff1 Work":0,"Difficulty Accepted":0.00000000,"Difficulty Rejected":0.00000000,"Last Share Difficulty":0.00000000,"Last Valid Work":14,"Device Hardware%":0.0000,"Device Rejected%":0.0000,"Device Elapsed":6056}],"id":1}`,
	}
	if version != "" {
		r["version"] = version
	}
	return r
}

const nano3sVersion = `{"STATUS":[{"STATUS":"S","When":1795314554,"Code":22,"Msg":"CGMiner versions","Description":"cgminer 4.11.1"}],"VERSION":[{"CGMiner":"4.11.1","API":"3.7","PROD":"Avalon Nano3s","MODEL":"Nano3s","HWTYPE":"N_MM1v1_X1","SWTYPE":"MM319","LVERSION":"25061101_97e23a6","BVERSION":"25061101_97e23a6","CGVERSION":"25061101_97e23a6","DNA":"0201000099bd7915","MAC":"40a5ef7113c5","UPAPI":"2"}],"id":1}`

func TestFetchNano3S(t *testing.T) {
	// With the version reply, and without it (model then comes from Ver[Nano3s-…]).
	for _, ver := range []string{nano3sVersion, ""} {
		s := Fetch(fakeMiner(t, nano3sReplies(ver)), 2*time.Second)
		if !s.OK {
			t.Fatalf("not ok: %v", s.QueryErrors)
		}
		eq := func(name string, got *float64, want float64) {
			t.Helper()
			if got == nil || (*got-want) > 1e-6 || (want-*got) > 1e-6 {
				t.Errorf("version=%v %s = %v, want %v", ver != "", name, got, want)
			}
		}
		eq("watts", s.Power.Watts, 94)
		eq("volts", s.Power.VoltageV, 27.482)
		eq("mpo", s.Power.MPOW, 95)
		eq("temp max", s.Temps.Max, 93)
		eq("temp avg", s.Temps.Avg, 85)
		eq("target", s.Temps.Target, 85)
		eq("outlet", s.Temps.Outlet, 64)
		eq("fan rpm", s.Fan.RPM, 1140)
		eq("fan pct", s.Fan.Percent, 26)
		eq("dh", s.Shares.DHPct, 2.525)
		eq("ghs spd", s.Hashrate.GHsSpd, 5016.36)
		eq("ths 5m (no pool work yet)", s.Hashrate.THs5m, 0)
		if s.Temps.Ambient != nil {
			t.Errorf("ambient should be absent, got %v", *s.Temps.Ambient)
		}
		if s.Power.PSOK == nil || !*s.Power.PSOK {
			t.Errorf("ps ok %v", s.Power.PSOK)
		}
		// WORKMODE[1] WORKLEVEL[0]: the owner set Medium in the Avalon app.
		if s.Power.WorkLevel == nil || *s.Power.WorkLevel != 1 || s.Control == nil || s.Control.WorkName != "medium" {
			t.Errorf("work level %v control %+v", s.Power.WorkLevel, s.Control)
		}
		if s.Control.LEDMode != "fixed" || s.Control.LEDHex != "#0000ff" {
			t.Errorf("led %+v", s.Control)
		}
		if s.Identity.Firmware != "25061101_97e23a6" || s.Identity.Chips != 12 || s.Identity.Core != "A3197S" || s.Identity.ID != "AVALON0" {
			t.Errorf("identity %+v", s.Identity)
		}
		if len(s.Chips.Temps) != 12 || s.Chips.Temps[0] != 78 || s.Chips.Temps[4] != 92 || len(s.Chips.VoltsMV) != 12 {
			t.Errorf("chips %+v", s.Chips)
		}
		if len(s.Errors.NetFail) != 0 || s.Uptime.Seconds != 6056 {
			t.Errorf("netfail/uptime %+v %+v", s.Errors.NetFail, s.Uptime)
		}
		if len(s.Warnings) != 0 {
			t.Errorf("Nano 3S fixture should stay green, got %v", s.Warnings)
		}
	}
}

func TestPSPowerLayouts(t *testing.T) {
	cases := []struct {
		ps     []float64
		nano3s bool
		w, v   float64
	}{
		{[]float64{0, 0, 0, 4, 2756, 126, 328}, false, 126, 27.56},  // Nano 3
		{[]float64{0, 0, 27482, 3, 0, 3444, 94}, true, 94, 27.482},  // Nano 3S
		{[]float64{0, 0, 27482, 3, 0, 3444, 94}, false, 94, 27.482}, // 3S layout, model unknown
		{[]float64{0, 0, 0, 4, 2756, 126}, false, 126, 27.56},       // older 6-field
	}
	for _, c := range cases {
		w, v, ok := psPower(c.ps, c.nano3s)
		if !ok || w != c.w || v != c.v {
			t.Errorf("psPower(%v, %v) = %v %v %v", c.ps, c.nano3s, w, v, ok)
		}
	}
	if _, _, ok := psPower([]float64{0, 0, 27482}, true); ok {
		t.Error("short 3S PS should not parse")
	}
}

func TestSupportsFanSpd(t *testing.T) {
	s3s := Fetch(fakeMiner(t, nano3sReplies(nano3sVersion)), 2*time.Second)
	if !s3s.OK || !SupportsFanSpd(s3s) {
		t.Fatalf("Nano 3S should offer Fan control: ok=%v support=%v err=%v", s3s.OK, SupportsFanSpd(s3s), s3s.QueryErrors)
	}
	// Ver prefix alone (no version command) still counts as 3S.
	sVer := Fetch(fakeMiner(t, nano3sReplies("")), 2*time.Second)
	if !SupportsFanSpd(sVer) {
		t.Fatal("Nano 3S Ver prefix should offer Fan control")
	}

	mmJSON := `"MM ID0":"` + nano3MM + `"`
	s3 := Fetch(fakeMiner(t, map[string]string{
		"summary": `{"STATUS":[{"STATUS":"S"}],"SUMMARY":[{"Elapsed":100}],"id":1}`,
		"version": `{"STATUS":[{"STATUS":"S"}],"VERSION":[{"CGMiner":"4.11.1","API":"3.7","PROD":"Avalonnano","MODEL":"nano3","HWTYPE":"PMMv1_X1","SWTYPE":"MM318_X2","LVERSION":"25103101_0736b2e"}],"id":1}`,
		"pools":   `{"STATUS":[{"STATUS":"S"}],"POOLS":[],"id":1}`,
		"devs":    `{"STATUS":[{"STATUS":"S"}],"DEVS":[{"ASC":0,"Name":"AVANANO"}],"id":1}`,
		"estats":  `{"STATUS":[{"STATUS":"S"}],"STATS":[{"STATS":0,"ID":"AVANANO0","Elapsed":100,` + mmJSON + `}],"id":1}`,
	}), 2*time.Second)
	if !s3.OK {
		t.Fatalf("nano3 fixture: %v", s3.QueryErrors)
	}
	if SupportsFanSpd(s3) {
		t.Fatal("original Nano 3 must hide Fan control")
	}

	mini := &model.Snapshot{Identity: model.Identity{Product: "Avalon Mini 3", Model: "Mini3"}}
	q := &model.Snapshot{Identity: model.Identity{Product: "Avalon Q", Model: "Q"}}
	if !SupportsFanSpd(mini) || !SupportsFanSpd(q) {
		t.Fatal("Mini 3 and Avalon Q should offer Fan control")
	}
	if SupportsFanSpd(nil) || SupportsFanSpd(&model.Snapshot{}) {
		t.Fatal("unknown/empty snapshot must hide Fan control")
	}
}
