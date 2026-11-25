package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

const testAddr = "bc1qtestaddrqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"

type integRig struct {
	t      *testing.T
	g      *Integrity
	al     *Alerts
	now    time.Time
	mu     sync.Mutex
	status int
	body   func() string
	hits   []string
	srv    *httptest.Server
	on     bool
}

func newIntegRig(t *testing.T) *integRig {
	r := &integRig{t: t, now: time.Unix(1795600000, 0), status: 200, on: true}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.hits = append(r.hits, q.URL.Path)
		if r.status != 200 {
			w.WriteHeader(r.status)
			fmt.Fprint(w, "upstream error for "+testAddr) // must never surface
			return
		}
		fmt.Fprint(w, r.body())
	}))
	t.Cleanup(r.srv.Close)
	dir := t.TempDir()
	r.al = NewAlerts(dir)
	r.al.Now = func() time.Time { return r.now }
	r.g = NewIntegrity(dir, r.al)
	r.g.Now = func() time.Time { return r.now }
	r.g.Enabled = func() bool { return r.on }
	r.g.syncFetch = true
	base := r.srv.URL
	r.g.Rewrite = func(s string) string { p, _ := url.Parse(s); return base + "/" + p.Host + p.EscapedPath() }
	return r
}

// ck returns a ckpool-style reply crediting h H/s with the last share `age` ago.
func (r *integRig) ck(h string, age time.Duration) func() string {
	return func() string {
		ls := r.now.Add(-age).Unix()
		return fmt.Sprintf(`{"hashrate1m":"%s","hashrate5m":"%s","hashrate1hr":"%s","lastshare":%d,"workers":1,"shares":12345,"bestshare":98765.4,
"worker":[{"workername":"%s.Nano3","hashrate1m":"%s","hashrate5m":"%s","hashrate1hr":"%s","lastshare":%d,"shares":12345,"bestshare":98765.4}]}`, h, h, h, ls, testAddr, h, h, h, ls)
	}
}

func (r *integRig) snap(active int, users ...string) *model.Snapshot {
	urls := []string{"stratum+tcp://solo.ckpool.org:3333", "stratum+tcp://eusolo.ckpool.org:3333", "stratum+tcp://public-pool.io:3333"}
	s := &model.Snapshot{OK: true, Hashrate: model.Hashrate{THs5m: fp(5.0)}}
	for i := range urls {
		u := testAddr + ".Nano3"
		if i < len(users) && users[i] != "" {
			u = users[i]
		}
		s.Pools = append(s.Pools, model.Pool{ID: i, URL: urls[i], User: u, Status: "Alive", StratumActive: i == active,
			LastShareUnix: r.now.Add(-4 * time.Second).Unix(), WorkDifficulty: fp(10000)})
	}
	return s
}

func (r *integRig) hist() []model.Sample {
	var out []model.Sample
	for t := r.now.Add(-time.Hour); !t.After(r.now); t = t.Add(time.Minute) {
		out = append(out, model.Sample{T: t.Unix(), H5m: fp(5.0)})
	}
	return out
}

func (r *integRig) observe(s *model.Snapshot) { r.g.Observe("m1", "Nano", s, r.hist()) }

// run advances the clock in poll-sized steps, observing each time.
func (r *integRig) run(d time.Duration, mk func() *model.Snapshot) {
	for end := r.now.Add(d); r.now.Before(end); r.now = r.now.Add(30 * time.Second) {
		r.observe(mk())
	}
}

func (r *integRig) alerts() []Alert { a, _ := r.al.Since(0, 0); return a }

func (r *integRig) noLeak() {
	r.t.Helper()
	v, _ := json.Marshal(r.g.View("m1"))
	a, _ := json.Marshal(r.alerts())
	for _, s := range []string{string(v), string(a)} {
		if strings.Contains(s, testAddr) || strings.Contains(s, "bc1q") {
			r.t.Fatalf("payout address leaked: %s", s)
		}
	}
}

func TestParseSIRate(t *testing.T) {
	for in, want := range map[string]float64{"217G": 217e9, "1.44T": 1.44e12, "379M": 379e6, "0": 0, "": 0, "5.1P": 5.1e15, "bogus": 0} {
		if got := parseSIRate(in); fmt.Sprintf("%.6g", got) != fmt.Sprintf("%.6g", want) {
			t.Errorf("%q: got %g want %g", in, got, want)
		}
	}
	if parseSIRate(1.5e12) != 1.5e12 {
		t.Error("numbers pass through")
	}
}

func TestParseCKPoolWorkerMatch(t *testing.T) {
	body := `{"hashrate1m":"9T","hashrate5m":"9T","hashrate1hr":"9T","lastshare":100,"workers":2,"shares":5,"bestshare":1,
"worker":[{"workername":"ADDR.Nano3","hashrate1m":"5T","hashrate5m":"4.9T","hashrate1hr":"4.8T","lastshare":90,"shares":3,"bestshare":7}]}`
	ps, err := parseCKPool([]byte(body), "ADDR.Nano3")
	if err != nil || !ps.Matched || ps.H1hr != 4.8e12 || ps.LastShare != 90 || ps.Best != 7 {
		t.Fatalf("worker match: %+v %v", ps, err)
	}
	ps, _ = parseCKPool([]byte(body), "ADDR.other")
	if ps.Matched || ps.H1hr != 9e12 || ps.Workers != 2 {
		t.Fatalf("address totals: %+v", ps)
	}
	if _, err := parseCKPool([]byte(`{"error":"x"}`), "a"); err == nil {
		t.Fatal("reply without lastshare must be an error")
	}
	if _, err := parseCKPool([]byte(`<html>`), "a"); err == nil {
		t.Fatal("html must be an error")
	}
}

func TestParsePublicPool(t *testing.T) {
	body := `{"bestDifficulty":123.4,"workersCount":2,"workers":[
{"sessionId":"a","name":"Nano3","bestDifficulty":"55.00","hashRate":4900000000000,"startTime":"2026-11-25T10:00:00.000Z","lastSeen":"2026-11-25T12:00:00.000Z"},
{"sessionId":"b","name":"bitaxe","bestDifficulty":"1.00","hashRate":"1200000000000","startTime":"2026-11-25T10:00:00.000Z","lastSeen":"2026-11-25T12:05:00.000Z"}]}`
	ps, err := parsePublicPool([]byte(body), "Nano3")
	if err != nil || !ps.Matched || ps.H1hr != 4.9e12 || ps.LastShare != time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("matched: %+v %v", ps, err)
	}
	ps, _ = parsePublicPool([]byte(body), "")
	if ps.Matched || ps.H5m != 6.1e12 || ps.Workers != 2 {
		t.Fatalf("all workers: %+v", ps)
	}
}

func TestPayoutAddrAndSources(t *testing.T) {
	if a, w, ok := payoutAddr(testAddr + ".Nano3"); !ok || a != testAddr || w != "Nano3" {
		t.Fatalf("got %q %q %v", a, w, ok)
	}
	for _, bad := range []string{"", "avalonaging", "user.worker", "bc1q!!", "../../etc"} {
		if _, _, ok := payoutAddr(bad); ok {
			t.Errorf("%q accepted as an address", bad)
		}
	}
	cases := map[string]string{
		"stratum+tcp://solo.ckpool.org:3333":    "solo.ckpool.org",
		"stratum+tcp://eusolo.ckpool.org:3333":  "eusolo.ckpool.org",
		"stratum+tcp://stratum.ckpool.org:3333": "raw.stats.ckpool.org",
		"stratum+tls://public-pool.io:4333":     "public-pool.io",
	}
	for u, want := range cases {
		s := statsSources(u, testAddr)
		if len(s) == 0 || s[0].label != want {
			t.Errorf("%s: %+v", u, s)
		}
	}
	if s := statsSources("stratum+tcp://solo.ckpool.org:3333", testAddr); len(s) != 2 || !s[1].lag {
		t.Error("regional ckpool falls back to the aggregated page")
	}
	if statsSources("stratum+tcp://mine.ocean.xyz:3334", testAddr) != nil {
		t.Error("unknown pools are unsupported")
	}
	if poolScheme("stratum+tcp://x:1") != "plain" || poolScheme("stratum+ssl://x:1") != "tls" || poolScheme("stratum2+tcp://x:1/k") != "sv2" {
		t.Error("scheme")
	}
}

func TestIntegrityHealthy(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("4.9T", 20*time.Second)
	r.run(2*time.Hour, func() *model.Snapshot { return r.snap(0) })
	v := r.g.View("m1")
	if v["state"] != "ok" {
		t.Fatalf("state %v: %v", v["state"], v["reason"])
	}
	if !strings.Contains(v["reason"].(string), "credits 4.90 TH/s") {
		t.Errorf("reason: %v", v["reason"])
	}
	if len(r.alerts()) != 0 {
		t.Fatalf("no alerts expected, got %+v", r.alerts())
	}
	// Rate limit: 2 h at one fetch per 5 min (first after 45 s) is about 24 requests.
	if n := len(r.hits); n < 20 || n > 26 {
		t.Fatalf("fetches: %d", n)
	}
	if !strings.HasPrefix(r.hits[0], "/solo.ckpool.org/users/") {
		t.Errorf("path %s", r.hits[0])
	}
	if v["scheme"] != "plain" || v["pool_host"] != "solo.ckpool.org:3333" {
		t.Errorf("view: %+v", v)
	}
	r.noLeak()
}

func TestIntegrityStaleAlertsAfterSustainAndRecovers(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("4.9T", 20*time.Second)
	r.run(90*time.Minute, func() *model.Snapshot { return r.snap(0) })
	// Hijack: the miner still sees accepted shares, the pool sees nothing new.
	frozen := r.now.Unix()
	r.body = func() string {
		return fmt.Sprintf(`{"hashrate1m":"0","hashrate5m":"0","hashrate1hr":"1.2T","lastshare":%d,"workers":1,"shares":1,"bestshare":1,"worker":[]}`, frozen)
	}
	r.run(15*time.Minute, func() *model.Snapshot { return r.snap(0) })
	if r.g.View("m1")["state"] != "mismatch" {
		t.Fatalf("expected mismatch: %v", r.g.View("m1"))
	}
	if len(r.alerts()) != 0 {
		t.Fatalf("must not alert before the sustain window: %+v", r.alerts())
	}
	r.run(15*time.Minute, func() *model.Snapshot { return r.snap(0) })
	al := r.alerts()
	if len(al) != 1 || al[0].Kind != "integrity" || al[0].Level != "bad" || !strings.Contains(al[0].Title, "isn't crediting") {
		t.Fatalf("alerts: %+v", al)
	}
	r.run(20*time.Minute, func() *model.Snapshot { return r.snap(0) })
	if len(r.alerts()) != 1 {
		t.Fatal("fires once per episode")
	}
	r.body = r.ck("4.9T", 10*time.Second)
	r.run(6*time.Minute, func() *model.Snapshot { return r.snap(0) })
	al = r.alerts()
	if len(al) != 2 || al[1].Level != "good" {
		t.Fatalf("recovery: %+v", al)
	}
	r.noLeak()
}

func TestIntegrityLowHashrate(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("600G", 10*time.Second) // fresh shares but ~12% of the miner's rate
	r.run(2*time.Hour, func() *model.Snapshot { return r.snap(0) })
	v := r.g.View("m1")
	if v["state"] != "mismatch" || !strings.Contains(v["reason"].(string), "credits only") {
		t.Fatalf("view: %v", v)
	}
	if len(r.alerts()) != 1 {
		t.Fatalf("alerts: %+v", r.alerts())
	}
}

func TestIntegrityNoHashCompareBeforeAnHour(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("600G", 10*time.Second)
	r.run(60*time.Minute, func() *model.Snapshot { return r.snap(0) })
	if r.g.View("m1")["state"] != "ok" || len(r.alerts()) != 0 {
		t.Fatalf("pool 1 h average needs time to build: %v %v", r.g.View("m1"), r.alerts())
	}
}

func TestIntegrityAPIDownIsUnknown(t *testing.T) {
	r := newIntegRig(t)
	r.status = 503
	r.run(3*time.Hour, func() *model.Snapshot { return r.snap(0) })
	v := r.g.View("m1")
	if v["state"] != "unknown" || !strings.Contains(v["reason"].(string), "HTTP 503") {
		t.Fatalf("view: %v", v)
	}
	if len(r.alerts()) != 0 {
		t.Fatalf("API down must not alert: %+v", r.alerts())
	}
	// Failed fetches back off to 10 min.
	if n := len(r.hits); n > 2*19 { // 2 sources per try
		t.Fatalf("too many requests while down: %d", n)
	}
	r.noLeak()
	// A dead host: the client error embeds the URL; the view must not.
	r.srv.Close()
	r.now = r.now.Add(11 * time.Minute)
	r.observe(r.snap(0))
	if s := r.g.View("m1")["reason"].(string); !strings.Contains(s, "no connection") {
		t.Fatalf("reason: %s", s)
	}
	r.noLeak()
}

func TestIntegrityMinerNotHashingIsUnknown(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("0", 3*time.Hour)
	r.run(2*time.Hour, func() *model.Snapshot {
		s := r.snap(0)
		s.Hashrate.THs5m = fp(0)
		s.Pools[0].LastShareUnix = r.now.Add(-time.Hour).Unix()
		return s
	})
	if r.g.View("m1")["state"] != "unknown" || len(r.alerts()) != 0 {
		t.Fatalf("%v %v", r.g.View("m1"), r.alerts())
	}
}

func TestIntegrityUnsupportedPool(t *testing.T) {
	r := newIntegRig(t)
	r.run(30*time.Minute, func() *model.Snapshot {
		s := r.snap(0)
		s.Pools[0].URL = "stratum+tcp://mine.ocean.xyz:3334"
		return s
	})
	if r.g.View("m1")["state"] != "unsupported" || len(r.hits) != 0 {
		t.Fatalf("%v hits=%d", r.g.View("m1"), len(r.hits))
	}
}

func TestIntegrityToggleOffStopsFetching(t *testing.T) {
	r := newIntegRig(t)
	r.on = false
	r.body = r.ck("4.9T", time.Second)
	r.run(time.Hour, func() *model.Snapshot { return r.snap(0) })
	if len(r.hits) != 0 || r.g.View("m1")["state"] != "off" {
		t.Fatalf("hits=%d view=%v", len(r.hits), r.g.View("m1"))
	}
}

func TestIntegritySettingsChange(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("4.9T", time.Second)
	r.run(5*time.Minute, func() *model.Snapshot { return r.snap(0) })
	other := "bc1qsomeoneelse00000000000000000000000000x.w"
	r.run(time.Minute, func() *model.Snapshot { return r.snap(0, other) })
	al := r.alerts()
	if len(al) != 1 || al[0].Level != "bad" || !strings.Contains(al[0].Body, "username changed") || strings.Contains(al[0].Body, "bc1q") {
		t.Fatalf("user change: %+v", al)
	}
	// An edit made from HashWatch is logged as info.
	r.g.Expect("m1", 2, "stratum+tcp://public-pool.io:4444", "")
	r.run(time.Minute, func() *model.Snapshot {
		s := r.snap(0, other)
		s.Pools[2].URL = "stratum+tcp://public-pool.io:4444"
		return s
	})
	al = r.alerts()
	if len(al) != 2 || al[1].Level != "info" || !strings.Contains(al[1].Body, "public-pool.io:4444 (was public-pool.io:3333)") {
		t.Fatalf("expected change: %+v", al)
	}
	r.noLeak()
}

func TestIntegrityFingerprintSurvivesRestart(t *testing.T) {
	r := newIntegRig(t)
	r.observe(r.snap(0))
	g2 := NewIntegrity(r.g.dir, r.al)
	g2.Now, g2.Enabled = r.g.Now, func() bool { return false }
	s := r.snap(0)
	s.Pools[1].URL = "stratum+tcp://evil.example:3333"
	g2.Observe("m1", "Nano", s, nil)
	if al := r.alerts(); len(al) != 1 || !strings.Contains(al[0].Body, "evil.example:3333") {
		t.Fatalf("%+v", al)
	}
}

func TestIntegrityPoolSwitchDebounced(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("4.9T", time.Second)
	r.run(10*time.Minute, func() *model.Snapshot { return r.snap(0) })
	r.observe(r.snap(1)) // a single blip doesn't count
	r.observe(r.snap(0))
	if len(r.alerts()) != 0 {
		t.Fatalf("blip: %+v", r.alerts())
	}
	r.run(2*time.Minute, func() *model.Snapshot { return r.snap(1) })
	al := r.alerts()
	if len(al) != 1 || !strings.Contains(al[0].Body, "Pool 2 (eusolo.ckpool.org:3333), was Pool 1 (solo.ckpool.org:3333)") {
		t.Fatalf("%+v", al)
	}
	if r.g.View("m1")["pool_host"] != "eusolo.ckpool.org:3333" {
		t.Fatal("view follows the active pool")
	}
}

func TestIntegrityDifficultySpike(t *testing.T) {
	r := newIntegRig(t)
	r.body = r.ck("4.9T", time.Second)
	r.run(5*time.Minute, func() *model.Snapshot { return r.snap(0) })
	r.run(2*time.Minute, func() *model.Snapshot { s := r.snap(0); s.Pools[0].WorkDifficulty = fp(536870912); return s })
	al := r.alerts()
	if len(al) != 1 || al[0].Level != "bad" || !strings.Contains(al[0].Body, "536,870,912") {
		t.Fatalf("%+v", al)
	}
	r.run(time.Minute, func() *model.Snapshot { return r.snap(0) })
	if al = r.alerts(); len(al) != 2 || al[1].Level != "good" {
		t.Fatalf("%+v", al)
	}
}

func TestIntegrityMockEnvLoopbackOnly(t *testing.T) {
	t.Setenv("HASHWATCH_POOLSTATS_MOCK", "http://example.com:1")
	if NewIntegrity(t.TempDir(), nil).Rewrite != nil {
		t.Fatal("non-loopback mock must be ignored")
	}
	t.Setenv("HASHWATCH_POOLSTATS_MOCK", "http://127.0.0.1:9")
	g := NewIntegrity(t.TempDir(), nil)
	if g.Rewrite == nil || g.Rewrite("https://solo.ckpool.org/users/x") != "http://127.0.0.1:9/solo.ckpool.org/users/x" {
		t.Fatal("loopback mock")
	}
}
