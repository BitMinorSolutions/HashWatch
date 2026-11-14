package app

import (
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
)

// Settings are validated on the server whatever the page sends.
func TestAutomationInputValidation(t *testing.T) {
	ts, s, _ := secServer(t)
	s.Cfg.Miners = []Miner{{ID: "m1", Name: "x", Host: "127.0.0.1", Port: 1, Kind: "cgminer"}}
	c := &tclient{t: t, ts: ts}
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	bad := []string{
		`{"miner":"m1","schedule":{"on":true,"rules":[{"days":[1],"start":"09:00","end":"10:00","mode":"turbo"}]}}`,
		`{"miner":"m1","schedule":{"on":true,"default":"3"}}`,
		`{"miner":"m1","schedule":{"on":true,"rules":[{"days":[9],"start":"09:00","end":"10:00","mode":"low"}]}}`,
		`{"miner":"m1","schedule":{"on":true,"rules":[{"days":[1],"start":"25:00","end":"10:00","mode":"low"}]}}`,
	}
	for _, b := range bad {
		if r, out := c.do("POST", "/api/features", b); r.StatusCode != 400 {
			t.Errorf("accepted %s → %d %v", b, r.StatusCode, out)
		}
	}
	if m, _ := s.Cfg.minerByID("m1"); m.Opts.Schedule.On {
		t.Fatal("rejected settings were saved")
	}
	r, out := c.do("POST", "/api/features", `{"miner":"m1","schedule":{"on":true,"default":"Medium","rules":[{"days":[1,2],"start":"22:00","end":"06:00","mode":"low"}]}}`)
	if r.StatusCode != 200 {
		t.Fatalf("valid schedule refused: %d %v", r.StatusCode, out)
	}
	if m, _ := s.Cfg.minerByID("m1"); m.Opts.Schedule.Default != "medium" {
		t.Fatal("default speed normalised")
	}
	// settings of removed features (throttle, failover, auto-restart) are ignored
	if r, _ := c.do("POST", "/api/features", `{"miner":"m1","throttle":{"on":true},"failover":{"on":true},"restart":{"on":true}}`); r.StatusCode != 200 {
		t.Fatalf("unknown field: %d", r.StatusCode)
	}
}

// "Running hot" uses the miner's own target, or a cautious 90 °C when it reports none.
func TestHotAlertUsesMinerTargetOrFallback(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		name        string
		target      *float64
		tmax        float64
		fire        bool
		bodyContain string
	}{
		{"cool unit over its target", f(80), 84, true, "80°C target"},
		{"hot-tuned unit under its target", f(95), 93, false, ""},
		{"no target, under fallback", nil, 89, false, ""},
		{"no target, over fallback", nil, 92, true, "90°C safe limit"},
	} {
		a := NewAlerts(t.TempDir())
		now := time.Unix(1794642000, 0)
		a.Now = func() time.Time { return now }
		for i := 0; i < 8; i++ {
			snap := &model.Snapshot{OK: true}
			snap.Temps.Max, snap.Temps.Target = f(c.tmax), c.target
			a.Observe("m", "Nano", snap, nil)
			now = now.Add(time.Minute)
		}
		items, _ := a.Since(0, 50)
		got := ""
		for _, al := range items {
			if al.Kind == "temp" {
				got = al.Body
			}
		}
		if (got != "") != c.fire || (c.fire && !strings.Contains(got, c.bodyContain)) {
			t.Errorf("%s: fired=%q", c.name, got)
		}
	}
}

// Each undecided feature switches off cleanly from internal/app/optional.go.
func TestOptionalSwitches(t *testing.T) {
	saved := Optional
	t.Cleanup(func() { Optional = saved })
	Optional = OptionalFeatures{}
	r := newAutoRig(t)
	r.opts(func(o *MinerOpts) { o.Light.Lamp, o.Light.BestFlash = true, true })
	r.a.Event("s", "best")
	for i := 0; i < 4; i++ {
		r.step(time.Minute)
	}
	if r.sent("ascset|0,ledset") != 0 {
		t.Fatalf("lamp/best flash switched off but light changed: %v", r.m.Log())
	}
	st := NewStats(t.TempDir())
	if v := st.View("s", netdata.Snapshot{}); len(v.Badges) != 0 || v.Streak != 0 {
		t.Fatal("badges switched off")
	}
	s := &Server{Cfg: r.cfg, Poll: NewPoller(t.TempDir(), time.Hour)}
	if s.SparklinePNG(44, 22) != nil {
		t.Fatal("sparkline switched off")
	}
	// the block gold flash is not optional
	r.opts(func(o *MinerOpts) { o.Light.Celebrate = true })
	r.a.Event("s", "block")
	r.step(time.Second)
	if r.sent("ascset|0,ledset") != 1 {
		t.Fatalf("block flash should still work: %v", r.m.Log())
	}
}
