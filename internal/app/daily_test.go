package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/internal/source"
	"github.com/hashwatch/hashwatch/web"
)

func makeHist(end time.Time, every time.Duration, hours float64, gapStart, gapEnd time.Time, ths, tmax, spike float64) []model.Sample {
	start := end.Add(-time.Duration(hours * float64(time.Hour)))
	var out []model.Sample
	for t := start; !t.After(end); t = t.Add(every) {
		if !gapStart.IsZero() && !t.Before(gapStart) && t.Before(gapEnd) {
			continue
		}
		s := model.Sample{T: t.Unix(), H5m: f64(ths), TMax: f64(tmax)}
		if spike > tmax && t.Hour() == gapEnd.Hour() && t.Minute() == 10 {
			s.TMax = f64(spike)
		}
		out = append(out, s)
	}
	// guarantee a spike even if the hour/minute trick missed
	if spike > tmax && len(out) > 10 {
		out[len(out)/2].TMax = f64(spike)
	}
	return out
}

func TestParseDailyAt(t *testing.T) {
	h, m, ok := parseDailyAt("")
	if !ok || h != 9 || m != 0 {
		t.Fatalf("default %d:%d %v", h, m, ok)
	}
	n, err := normalizeDailyAt("18:30")
	if err != nil || n != "18:30" {
		t.Fatalf("18:30 %q %v", n, err)
	}
	if _, err := normalizeDailyAt("25:00"); err == nil {
		t.Fatal("25:00 should fail")
	}
}

func TestDailySummaryAtNineAndOncePerDay(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 11, 14, 8, 59, 0, 0, loc)
	st := NewStats(t.TempDir())
	st.Now = func() time.Time { return now }
	st.Alerts = NewAlerts(t.TempDir())
	st.Net = func() netdata.Snapshot { return netdata.Snapshot{Difficulty: 134.8238373232727e12} }
	gapS := time.Date(2026, 11, 14, 2, 0, 0, 0, loc)
	gapE := time.Date(2026, 11, 14, 3, 0, 0, 0, loc)
	hist := makeHist(time.Date(2026, 11, 14, 8, 59, 0, 0, loc), 2*time.Minute, 24, gapS, gapE, 4, 70, 81)
	snap := &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs15m: f64(4)}, Shares: model.Shares{BestShare: f64(2.2e9)}, Temps: model.Temps{Max: f64(70)}}
	st.mu.Lock()
	ms := st.get("m")
	ms.Leaderboard = []ShareRec{{Diff: 2.2e9, T: now.Add(-2 * time.Hour).Unix()}}
	st.mu.Unlock()

	st.MaybeDailySummary("m", "Nano", snap, hist, "")
	if items, _ := st.Alerts.Since(0, 0); len(items) != 0 {
		t.Fatalf("fired before 09:00: %+v", items)
	}

	now = time.Date(2026, 11, 14, 9, 0, 0, 0, loc)
	hist = makeHist(now, 2*time.Minute, 24, gapS, gapE, 4, 70, 81)
	st.MaybeDailySummary("m", "Nano", snap, hist, "")
	items, _ := st.Alerts.Since(0, 0)
	if len(items) != 1 || items[0].Kind != "summary" || !strings.Contains(items[0].Title, "Nano") {
		t.Fatalf("expected one summary, got %+v", items)
	}
	body := items[0].Body
	for _, want := range []string{"Online", "%", "4.00 TH/s", "81°C", "2.20G", "1 in", "to a block"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	// uptime is about 23/24 after a 1-hour gap (around 96%)
	if !strings.Contains(body, "96%") && !strings.Contains(body, "95%") && !strings.Contains(body, "94%") {
		t.Fatalf("uptime not ~95%%: %s", body)
	}

	now = time.Date(2026, 11, 14, 9, 1, 0, 0, loc)
	st.MaybeDailySummary("m", "Nano", snap, hist, "")
	if items, _ = st.Alerts.Since(0, 0); len(items) != 1 {
		t.Fatalf("second fire same day: %d", len(items))
	}

	// next morning
	now = time.Date(2026, 11, 15, 9, 0, 0, 0, loc)
	hist = makeHist(now, 2*time.Minute, 24, time.Time{}, time.Time{}, 4, 70, 72)
	st.MaybeDailySummary("m", "Nano", snap, hist, "")
	if items, _ = st.Alerts.Since(0, 0); len(items) != 2 {
		t.Fatalf("next day: %d", len(items))
	}
}

func TestDailySummaryCustomTimeAndMidnightUnchanged(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, loc)
	st := NewStats(t.TempDir())
	st.Now = func() time.Time { return now }
	st.Alerts = NewAlerts(t.TempDir())
	snap := func(best, blocks float64, up int64) *model.Snapshot {
		return &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs5m: f64(4)}, Shares: model.Shares{BestShare: f64(best), FoundBlocks: f64(blocks)},
			Uptime: model.Uptime{Seconds: up}}
	}
	st.Observe("m", "Nano", snap(5e8, 0, 1000))
	for i := 1; i <= 72; i++ {
		now = now.Add(5 * time.Second)
		st.Observe("m", "Nano", snap(5e8, 0, 1000+int64(i*5)))
	}
	// 18:30 custom time: 10:00 should not fire the briefing
	hist := makeHist(now, 2*time.Minute, 24, time.Time{}, time.Time{}, 4, 70, 70)
	st.MaybeDailySummary("m", "Nano", snap(5e8, 0, 1360), hist, "18:30")
	if items, _ := st.Alerts.Since(0, 0); len(items) != 0 {
		t.Fatalf("fired at 10:00 for 18:30: %+v", items)
	}
	now = time.Date(2026, 11, 14, 18, 30, 0, 0, loc)
	st.MaybeDailySummary("m", "Nano", snap(5e8, 0, 1360), hist, "18:30")
	items, _ := st.Alerts.Since(0, 0)
	var daily int
	for _, a := range items {
		if a.Kind == "summary" && strings.HasPrefix(a.Title, "Daily summary") {
			daily++
		}
	}
	if daily != 1 {
		t.Fatalf("18:30 briefing %d items %+v", daily, items)
	}
	// midnight rollover still produces the yesterday digest
	now = time.Date(2026, 11, 15, 0, 0, 30, 0, loc)
	st.Observe("m", "Nano", snap(5e8, 0, 30))
	items, _ = st.Alerts.Since(0, 0)
	var yesterday int
	for _, a := range items {
		if a.Kind == "summary" && strings.HasPrefix(a.Title, "Yesterday") {
			yesterday++
		}
	}
	if yesterday != 1 {
		t.Fatalf("midnight digest missing: %+v", items)
	}
}

func TestDailySummarySettingsAndNtfy(t *testing.T) {
	h, got, mu := mockNtfy(t)
	defaultNtfy.reset()
	defaultNtfy.client = h.Client()
	t.Cleanup(func() {
		defaultNtfy.reset()
		defaultNtfy.client = &http.Client{Timeout: 8 * time.Second}
	})

	cfg, _ := LoadConfig(t.TempDir())
	cfg.SetPassword("mainpass1")
	al := NewAlerts(t.TempDir())
	st := NewStats(t.TempDir())
	st.Alerts = al
	loc := time.Local
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, loc)
	st.Now = func() time.Time { return now }
	st.Net = func() netdata.Snapshot { return netdata.Snapshot{Difficulty: 134.8238373232727e12} }
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), 0), Static: web.FS, Port: 48412, InstanceID: NewID(), Alerts: al, Stats: st}
	al.Push = s.NtfyPush
	ts := newTestServerFrom(t, s)
	c := &tclient{t: t, ts: ts}
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	r, out := c.do("GET", "/api/alerts", "")
	if r.StatusCode != 200 || out["daily_summary_at"] != "09:00" {
		t.Fatalf("get default %d %v", r.StatusCode, out)
	}
	r, out = c.do("POST", "/api/alerts/settings", `{"daily_summary_at":"07:15"}`)
	if r.StatusCode != 200 || out["daily_summary_at"] != "07:15" {
		t.Fatalf("set %d %v", r.StatusCode, out)
	}
	if cfg.DailySummaryAt != "07:15" {
		t.Fatalf("saved %q", cfg.DailySummaryAt)
	}
	r, out = c.do("POST", "/api/alerts/settings", `{"daily_summary_at":"99:99"}`)
	if r.StatusCode != 400 {
		t.Fatalf("bad time %d %v", r.StatusCode, out)
	}

	// Enable ntfy against the mock, then fire a 9am briefing (cfg time is 07:15, so move clock).
	body, _ := json.Marshal(map[string]any{"enabled": true, "server": h.URL, "token": "daily-secret"})
	if r, out = c.do("POST", "/api/ntfy", string(body)); r.StatusCode != 200 {
		t.Fatalf("ntfy %d %v", r.StatusCode, out)
	}
	now = time.Date(2026, 11, 14, 7, 15, 0, 0, loc)
	hist := makeHist(now, 2*time.Minute, 24, time.Time{}, time.Time{}, 4, 70, 73)
	snap := &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs15m: f64(4), THs5m: f64(4)}, Shares: model.Shares{BestShare: f64(1.5e9)}, Temps: model.Temps{Max: f64(70)}}
	st.MaybeDailySummary("m", "Nano", snap, hist, cfg.DailySummaryAt)
	deadline := time.Now().Add(2 * time.Second)
	var payload ntfyReq
	for {
		mu.Lock()
		n := len(*got)
		if n > 0 {
			payload = (*got)[0]
		}
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ntfy did not receive the daily summary")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if payload.Priority != "3" {
		t.Fatalf("priority %q", payload.Priority)
	}
	if !strings.Contains(payload.Title, "Daily summary") || !strings.Contains(payload.Body, "TH/s") {
		t.Fatalf("payload %+v", payload)
	}
	if strings.Contains(payload.Body, "daily-secret") || strings.Contains(payload.Title, "daily-secret") {
		t.Fatalf("token leaked: %+v", payload)
	}
	if payload.Auth != "Bearer daily-secret" {
		t.Fatalf("auth %q", payload.Auth)
	}
	items, _ := al.Since(0, 0)
	if len(items) != 1 || items[0].Kind != "summary" {
		t.Fatalf("alerts list %+v", items)
	}
}

func TestHistorySince(t *testing.T) {
	p := NewPoller(t.TempDir(), time.Hour) // slow, we inject history
	p.mu.Lock()
	p.m["m"] = &minerState{History: []model.Sample{{T: 100}, {T: 200}, {T: 300}}}
	p.mu.Unlock()
	got := p.HistorySince("m", 200)
	if len(got) != 2 || got[0].T != 200 {
		t.Fatalf("%+v", got)
	}
}

func TestDailySummaryUsesSimulatorSnapshot(t *testing.T) {
	m := sim.New()
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	src := source.CGMiner{Addr: ln.Addr().String()}
	snap := src.Fetch()
	if snap == nil || !snap.OK {
		t.Fatalf("sim snap %+v", snap)
	}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	st := NewStats(t.TempDir())
	st.Now = func() time.Time { return now }
	st.Alerts = NewAlerts(t.TempDir())
	st.Net = func() netdata.Snapshot { return netdata.Snapshot{Difficulty: 134.8238373232727e12} }
	hs := 4.0
	if snap.Hashrate.THs5m != nil {
		hs = *snap.Hashrate.THs5m
	}
	tmax := 70.0
	if snap.Temps.Max != nil {
		tmax = *snap.Temps.Max
	}
	hist := makeHist(now, 2*time.Minute, 24, time.Time{}, time.Time{}, hs, tmax, tmax+5)
	st.MaybeDailySummary("m", "Nano", snap, hist, "09:00")
	items, _ := st.Alerts.Since(0, 0)
	if len(items) != 1 || items[0].Kind != "summary" || !strings.Contains(items[0].Body, "TH/s") || !strings.Contains(items[0].Body, "°C") {
		t.Fatalf("sim briefing %+v", items)
	}
}
