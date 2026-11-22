package cgminer

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

func first(r map[string]any, key string) map[string]any {
	if r == nil {
		return nil
	}
	if arr, ok := r[key].([]any); ok && len(arr) > 0 {
		m, _ := arr[0].(map[string]any)
		return m
	}
	return nil
}

func list(r map[string]any, key string) []map[string]any {
	var out []map[string]any
	if arr, ok := r[key].([]any); ok {
		for _, a := range arr {
			if m, ok := a.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}

func num(v any) *float64 {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case string:
		s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "%"))
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		f = x
	case bool:
		if t {
			f = 1
		}
	default:
		return nil
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

func scale(p *float64, k float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p * k
	return &v
}

func nums(s string) []float64 {
	var out []float64
	for _, f := range strings.Fields(s) {
		if x, err := strconv.ParseFloat(strings.TrimSuffix(f, "%"), 64); err == nil {
			out = append(out, x)
		}
	}
	return out
}

func intp(p *float64) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// bracket fields look like: Ver[nano3-2510...] Temp[41] PVT_T0[88  91 ...]
var mmField = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\[([^\]]*)\]`)

// ParseMM extracts the Avalon "MM ID0" key[value] pairs from an estats/stats reply.
func ParseMM(stats map[string]any) map[string]string {
	out := map[string]string{}
	for _, s := range list(stats, "STATS") {
		keys := make([]string, 0, len(s))
		for k := range s {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !strings.HasPrefix(k, "MM ID") {
				continue
			}
			for _, m := range mmField.FindAllStringSubmatch(str(s[k]), -1) {
				if _, seen := out[m[1]]; !seen {
					out[m[1]] = strings.TrimSpace(m[2])
				}
			}
		}
	}
	return out
}

func statsID(stats map[string]any) string {
	for _, s := range list(stats, "STATS") {
		for k := range s {
			if strings.HasPrefix(k, "MM ID") {
				return str(s["ID"])
			}
		}
	}
	return ""
}

func Human(sec int64) string {
	d, h, m := sec/86400, (sec%86400)/3600, (sec%3600)/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// Fetch reads every section from one miner and builds a snapshot.
func Fetch(addr string, timeout time.Duration) *model.Snapshot {
	s := &model.Snapshot{MinerHost: addr, Source: "cgminer", FetchedAt: time.Now().Unix(), MMFields: map[string]string{}}
	q := func(cmd string) map[string]any {
		r, err := Query(addr, cmd, timeout)
		if err != nil {
			s.QueryErrors = append(s.QueryErrors, cmd+": "+err.Error())
		}
		return r
	}
	summary := q("summary")
	if summary == nil {
		s.OK = false
		return s
	}
	version, pools, devs := q("version"), q("pools"), q("devs")
	stats, err := Query(addr, "estats", timeout)
	if err != nil || len(ParseMM(stats)) == 0 {
		if st, err2 := Query(addr, "stats", timeout); err2 == nil {
			stats = st
		}
	}
	Build(s, summary, version, pools, devs, stats)
	return s
}

// Build maps raw cgminer replies into the snapshot (exported for tests).
func Build(s *model.Snapshot, summary, version, pools, devs, stats map[string]any) {
	s.OK = summary != nil
	mm := ParseMM(stats)
	s.MMFields = mm
	f := func(k string) *float64 {
		if v, ok := mm[k]; ok {
			return num(v)
		}
		return nil
	}

	if v := first(version, "VERSION"); v != nil {
		s.Identity = model.Identity{Product: str(v["PROD"]), Model: str(v["MODEL"]), CGMiner: str(v["CGMiner"]), API: str(v["API"]),
			HWType: str(v["HWTYPE"]), SWType: str(v["SWTYPE"]), MAC: str(v["MAC"]), Firmware: str(v["LVERSION"])}
	}
	if ver := mm["Ver"]; ver != "" {
		if i := strings.Index(ver, "-"); i >= 0 {
			ver = ver[i+1:]
		}
		s.Identity.Firmware = ver
	}
	s.Identity.Hardware = mm["HVer"]
	s.Identity.Core = mm["Core"]
	if ta := f("TA"); ta != nil {
		s.Identity.Chips = int(*ta)
	}
	s.Identity.ID = statsID(stats)

	sm := first(summary, "SUMMARY")
	m := func(k string) *float64 { return num(sm[k]) }
	s.Hashrate = model.Hashrate{THs5s: scale(m("MHS 5s"), 1e-6), THs1m: scale(m("MHS 1m"), 1e-6), THs5m: scale(m("MHS 5m"), 1e-6),
		THs15m: scale(m("MHS 15m"), 1e-6), THsAvg: scale(m("MHS av"), 1e-6), GHsMM: f("GHSmm"), GHsAvg: f("GHSavg"), GHsSpd: f("GHSspd"), WU: f("WU")}
	s.Hashrate.THsHero = s.Hashrate.THs5m
	if s.Hashrate.THsHero == nil {
		s.Hashrate.THsHero = scale(s.Hashrate.GHsSpd, 1e-3)
	}
	if el := m("Elapsed"); el != nil {
		s.Uptime = model.Uptime{Seconds: int64(*el), Human: Human(int64(*el))}
	}
	s.Shares = model.Shares{Accepted: m("Accepted"), Rejected: m("Rejected"), Stale: m("Stale"), HWErrors: m("Hardware Errors"),
		RejectPct: m("Pool Rejected%"), StalePct: m("Pool Stale%"), DeviceHWPct: m("Device Hardware%"), DHPct: f("DH"),
		BestShare: m("Best Share"), Utility: m("Utility"), Getworks: m("Getworks"), GetFailures: m("Get Failures"),
		RemoteFailures: m("Remote Failures"), FoundBlocks: m("Found Blocks"), NetworkBlocks: m("Network Blocks")}

	s.Temps = model.Temps{Ambient: f("Temp"), Outlet: f("OTemp"), Avg: f("TAvg"), Max: f("TMax"), Target: f("TarT")}
	if s.Temps.Max == nil {
		s.Temps.Max = f("MTmax")
	}
	s.Fan = model.Fan{RPM: f("Fan1"), Percent: f("FanR")}
	ps := nums(mm["PS"])
	s.Power.PSRaw = ps
	if w, v, ok := psPower(ps, isNano3S(mm, s.Identity.Model)); ok {
		okPS := ps[0] == 0
		s.Power.Watts, s.Power.VoltageV, s.Power.PSOK = &w, &v, &okPS
	}
	s.Power.MPOW = f("MPO")
	s.Power.WorkLevel = intp(workLevel(mm))

	s.Chips = model.Chips{Temps: nums(mm["PVT_T0"]), VoltsMV: nums(mm["PVT_V0"]), Nonces: nums(mm["MW0"])}
	s.Errors = model.Errors{ECHU: nums(mm["ECHU"]), ECMM: f("ECMM"), HashStatus: intp(f("HashStatus")), SoftOff: f("SoftOFF"),
		CRC: f("CRC"), PingMS: f("PING"), LED: mm["Led"]}
	for _, u := range nums(mm["NETFAIL"]) {
		if u > 0 {
			s.Errors.NetFail = append(s.Errors.NetFail, model.NetFail{Unix: int64(u)})
		}
	}

	for _, p := range list(pools, "POOLS") {
		diff := num(p["Last Share Difficulty"])
		if diff == nil || *diff == 0 {
			diff = num(p["Work Difficulty"])
		}
		pl := model.Pool{URL: str(p["URL"]), StratumURL: str(p["Stratum URL"]), Status: str(p["Status"]), User: str(p["User"]),
			StratumActive: str(p["Stratum Active"]) == "true", Accepted: num(p["Accepted"]), Rejected: num(p["Rejected"]),
			Stale: num(p["Stale"]), RejectPct: num(p["Pool Rejected%"]), StalePct: num(p["Pool Stale%"]), Difficulty: diff,
			BestShare: num(p["Best Share"]), GetFailures: num(p["Get Failures"]), RemoteFailures: num(p["Remote Failures"])}
		if x := num(p["POOL"]); x != nil {
			pl.ID = int(*x)
		}
		if x := num(p["Priority"]); x != nil {
			pl.Priority = int(*x)
		}
		if x := num(p["Last Share Time"]); x != nil {
			pl.LastShareUnix = int64(*x)
		}
		if x := num(p["Current Block Height"]); x != nil {
			pl.BlockHeight = int64(*x)
		}
		s.Pools = append(s.Pools, pl)
	}
	if d := first(devs, "DEVS"); d != nil {
		s.Device = model.Device{Name: str(d["Name"]), Enabled: str(d["Enabled"]), Status: str(d["Status"])}
	}
	s.Control = ControlState(mm)
	s.Warnings = Warnings(s)
}

// isNano3S: the miner is an Avalon Nano 3S (MODEL "Nano3s", Ver[Nano3s-…]).
func isNano3S(mm map[string]string, model string) bool {
	return strings.EqualFold(model, "nano3s") || strings.HasPrefix(strings.ToLower(mm["Ver"]), "nano3s-")
}

// psPower reads watts and input volts from PS[]. Both models report 7 fields,
// laid out differently:
//
//	Nano 3:  PS[0 0 0 4 2756 126 328]   -> volts = [4]/100 (27.56 V), watts = [5] (126 W)
//	Nano 3S: PS[0 0 27482 3 0 3444 94]  -> volts = [2]/1000 (27.48 V), watts = [6] (94 W)
//
// When the model is unknown, the 3S layout is recognized by [2] set and [4] zero.
func psPower(ps []float64, nano3s bool) (watts, volts float64, ok bool) {
	if len(ps) >= 7 && (nano3s || (ps[2] > 0 && ps[4] == 0)) {
		return ps[6], ps[2] / 1000, true
	}
	if len(ps) >= 6 && !nano3s {
		return ps[5], ps[4] / 100, true
	}
	return 0, 0, false
}

func WorkName(l int) string {
	switch l {
	case 0:
		return "low"
	case 1:
		return "medium"
	case 2:
		return "high"
	}
	return fmt.Sprintf("level %d", l)
}

// Warnings derives the same health notices the original dashboard showed.
func Warnings(s *model.Snapshot) []string {
	var w []string
	if !s.OK {
		return w
	}
	if s.Temps.Max != nil && s.Temps.Target != nil && *s.Temps.Max > *s.Temps.Target {
		w = append(w, fmt.Sprintf("Max chip temperature %.0f°C is above the %.0f°C target.", *s.Temps.Max, *s.Temps.Target))
	}
	if s.Shares.DHPct != nil && *s.Shares.DHPct >= 1 {
		w = append(w, fmt.Sprintf("Chip error rate (DH) is %.3f%%.", *s.Shares.DHPct))
	}
	if s.Errors.HashStatus != nil && *s.Errors.HashStatus != 1 {
		w = append(w, fmt.Sprintf("Hash board reports status %d (not hashing).", *s.Errors.HashStatus))
	}
	if s.Fan.RPM != nil && *s.Fan.RPM == 0 {
		w = append(w, "Fan reports 0 RPM.")
	}
	if s.Power.PSOK != nil && !*s.Power.PSOK {
		w = append(w, "Power supply reports a fault.")
	}
	active := false
	for _, p := range s.Pools {
		if p.StratumActive {
			active = true
		}
	}
	if len(s.Pools) > 0 && !active {
		w = append(w, "No pool is currently active.")
	}
	return w
}

// LEDModes are the ledset "effect" values (Canaan Nano 3S API: 0=off 1=on 2=flash 3=breath 4=loop).
var LEDModes = []string{"off", "fixed", "flash", "pulse", "loop"}

func LEDModeName(n int) string {
	if n >= 0 && n < len(LEDModes) {
		return LEDModes[n]
	}
	return fmt.Sprintf("mode %d", n)
}

// workLevel is the work speed the owner set (0 low, 1 medium, 2 high).
// The Nano 3S reports both WORKMODE and WORKLEVEL; WORKMODE is the setting
// (what "ascset workmode,set,N" writes and the Avalon app shows) and WORKLEVEL
// stays 0 there. The Nano 3 reports only WORKLEVEL.
func workLevel(mm map[string]string) *float64 {
	if v := num(mm["WORKMODE"]); v != nil {
		return v
	}
	return num(mm["WORKLEVEL"])
}

// ControlState reads work level and light settings from the MM fields.
// See workLevel for the work speed. LEDUser[effect-bright-temper-r-g-b]
// is the light on firmwares that report it (Nano 3S); otherwise Led[n] gives the effect only.
func ControlState(mm map[string]string) *model.Control {
	c := &model.Control{}
	if lv := workLevel(mm); lv != nil {
		c.WorkLevel = intp(lv)
		c.WorkName = WorkName(*c.WorkLevel)
	}
	if u := strings.Split(mm["LEDUser"], "-"); len(u) == 6 {
		v := make([]int, 6)
		ok := true
		for i, x := range u {
			n, err := strconv.Atoi(strings.TrimSpace(x))
			if err != nil {
				ok = false
				break
			}
			v[i] = n
		}
		if ok {
			c.LEDMode = LEDModeName(v[0])
			c.LEDBright, c.LEDTemper = &v[1], &v[2]
			c.LEDHex = fmt.Sprintf("#%02x%02x%02x", v[3]&255, v[4]&255, v[5]&255)
		}
	}
	if c.LEDMode == "" {
		if n := num(mm["Led"]); n != nil {
			c.LEDMode = LEDModeName(int(*n))
		}
	}
	if c.WorkLevel == nil && c.LEDMode == "" {
		return nil
	}
	return c
}
