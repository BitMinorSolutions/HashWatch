package app

// Pool integrity: a read-only cross-check between what the miner says it is
// doing (cgminer summary / pools) and what the pool's public stats page
// credits to the payout address. It never sends anything to the miner.
//
// Why: plain Stratum V1 (stratum+tcp://) is unencrypted and unauthenticated.
// An attacker who reroutes the connection can pose as the pool; the miner keeps
// hashing and reports accepted shares, while the real pool sees nothing.
// (Braiins security alert, 25 Nov 2026.) The pool's own stats are the one
// place that shows it.
//
// Privacy: the payout address is used only inside the stats request URL. It is
// never stored, logged, returned by the API or put in an alert. Errors are
// reduced to short fixed strings so an http.Client error (which embeds the URL)
// can't leak it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

// Tunables (vars so tests can shorten them).
var (
	integEvery      = 5 * time.Minute  // pool stats fetch interval per miner
	integRetry      = 10 * time.Minute // after a failed fetch
	integFirstAfter = 45 * time.Second // first fetch after HashWatch starts / the active pool changes
	integStaleMin   = 20 * time.Minute // pool's last share older than this (or 10 expected share intervals) = stale
	integRawLag     = 15 * time.Minute // extra allowance when only the aggregated raw.stats page answered
	integLowRatio   = 0.25             // pool 1 h hashrate below 25% of the miner's own 1 h average = too low
	integLowAfter   = 75 * time.Minute // compare hashrates only after this long on the same pool
	integSustain    = 20 * time.Minute // a mismatch must last this long before it alerts
	integMinerFresh = 10 * time.Minute // miner's own last accepted share must be newer than this
	integDiffMax    = 16777216.0       // share difficulty above this is not something a pool sets for a home miner
	integTimeout    = 15 * time.Second
)

const integKind = "integrity"

// PoolStats is what a pool's public page credits (hashrates in H/s).
type PoolStats struct {
	H1m, H5m, H1hr float64
	LastShare      int64
	Shares         float64
	Best           float64
	Workers        int
	Matched        bool // stats are for this miner's worker, not the whole address
}

type integSource struct {
	label string // shown in the UI (host only)
	url   string // contains the address: never exposed
	kind  string // "ckpool" | "publicpool"
	lag   bool   // aggregated page that lags the regional one
}

type integState struct {
	fp         []string // per pool: url + "|" + short user hash
	haveFP     bool
	activeIdx  int
	activeKey  string
	activeURL  string
	activeUser string // kept in memory only, for the stats request
	activeAt   time.Time
	pendKey    string
	pendN      int
	diffHigh   int
	diffFired  bool
	fetching   bool
	nextFetch  time.Time
	checkedAt  time.Time
	stats      *PoolStats
	statsLabel string
	statsLag   bool
	fetchErr   string
	state      string
	reason     string
	badSince   time.Time
	badFired   bool
	minerH5m   float64
	minerH1h   float64
	minerLast  int64
	workDiff   float64
	plain      string // "plain" | "tls" | "sv2" for the active pool
	expect     []integExpect
}

type integExpect struct {
	idx      int
	url      string
	userHash string // "" = any
	until    time.Time
}

// Integrity runs the pool cross-check for every miner.
type Integrity struct {
	Alerts  *Alerts
	Enabled func() bool // the "integrity" alert toggle; off also stops the stats fetch
	Now     func() time.Time
	Client  *http.Client
	// Rewrite maps a real stats URL to a mock (tests and the QC sim only).
	Rewrite func(string) string
	Version string

	syncFetch bool // tests: fetch inline instead of in a goroutine
	dir       string
	mu        sync.Mutex
	st        map[string]*integState
}

func NewIntegrity(dir string, alerts *Alerts) *Integrity {
	g := &Integrity{Alerts: alerts, Now: time.Now, dir: dir, st: map[string]*integState{},
		Client: &http.Client{Timeout: integTimeout}}
	// QC hook: only a loopback mock is ever honoured.
	if base := os.Getenv("HASHWATCH_POOLSTATS_MOCK"); base != "" {
		if u, err := url.Parse(base); err == nil && isLoopbackHost(u.Hostname()) {
			b := strings.TrimRight(base, "/")
			if os.Getenv("HASHWATCH_POOLSTATS_FAST") == "1" { // QC only: 30x faster timers, mock mode only
				for _, d := range []*time.Duration{&integEvery, &integRetry, &integFirstAfter, &integStaleMin, &integRawLag, &integLowAfter, &integSustain} {
					*d /= 30
				}
			}
			g.Rewrite = func(s string) string {
				p, err := url.Parse(s)
				if err != nil {
					return s
				}
				return b + "/" + p.Host + p.EscapedPath()
			}
		}
	}
	return g
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (g *Integrity) on() bool { return g.Enabled == nil || g.Enabled() }

func userHash(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	h := sha256.Sum256([]byte("hashwatch-pool-user\x00" + u))
	return hex.EncodeToString(h[:6])
}

// poolHostPort is the URL without the scheme ("solo.ckpool.org:3333").
func poolHostPort(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	return strings.ToLower(u)
}

func poolHostOnly(u string) string {
	hp := poolHostPort(u)
	if h, _, err := net.SplitHostPort(hp); err == nil {
		return h
	}
	return hp
}

func poolScheme(u string) string {
	l := strings.ToLower(strings.TrimSpace(u))
	switch {
	case strings.HasPrefix(l, "stratum+ssl://"), strings.HasPrefix(l, "stratum+tls://"):
		return "tls"
	case strings.HasPrefix(l, "stratum2+"):
		return "sv2"
	case strings.Contains(l, "://") && !strings.HasPrefix(l, "stratum+tcp://"):
		return "other"
	}
	return "plain"
}

var btcAddrRe = regexp.MustCompile(`^((bc1|tb1)[0-9a-z]{8,87}|[13][1-9A-HJ-NP-Za-km-z]{25,34})$`)

// payoutAddr is the address part of a solo-pool username ("addr.worker").
func payoutAddr(user string) (addr, worker string, ok bool) {
	u := strings.TrimSpace(user)
	if i := strings.Index(u, "."); i >= 0 {
		addr, worker = u[:i], u[i+1:]
	} else {
		addr = u
	}
	if strings.HasPrefix(strings.ToLower(addr), "bc1") || strings.HasPrefix(strings.ToLower(addr), "tb1") {
		addr = strings.ToLower(addr)
	}
	return addr, worker, btcAddrRe.MatchString(addr)
}

var ckRegional = map[string]bool{"solo.ckpool.org": true, "uwsolo.ckpool.org": true, "uesolo.ckpool.org": true,
	"eusolo.ckpool.org": true, "sgsolo.ckpool.org": true, "ausolo.ckpool.org": true}

// statsSources lists where the active pool publishes per-address stats, best first.
func statsSources(poolURL, addr string) []integSource {
	h := poolHostOnly(poolURL)
	a := url.PathEscape(addr)
	switch {
	case ckRegional[h]:
		return []integSource{{label: h, url: "https://" + h + "/users/" + a, kind: "ckpool"},
			{label: "raw.stats.ckpool.org", url: "https://raw.stats.ckpool.org/users/" + a, kind: "ckpool", lag: true}}
	case h == "stratum.ckpool.org":
		// The auto-routing endpoint can land on any region: use the aggregate.
		return []integSource{{label: "raw.stats.ckpool.org", url: "https://raw.stats.ckpool.org/users/" + a, kind: "ckpool", lag: true}}
	case h == "public-pool.io" || h == "web.public-pool.io":
		return []integSource{{label: "public-pool.io", url: "https://public-pool.io:40557/api/client/" + a, kind: "publicpool"}}
	}
	return nil
}

// ckpool hashrates look like "217G", "1.44T", "379M", "0".
func parseSIRate(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		mul := 1.0
		switch s[len(s)-1] {
		case 'K', 'k':
			mul = 1e3
		case 'M':
			mul = 1e6
		case 'G':
			mul = 1e9
		case 'T':
			mul = 1e12
		case 'P':
			mul = 1e15
		case 'E':
			mul = 1e18
		case 'Z':
			mul = 1e21
		}
		if mul != 1 {
			s = s[:len(s)-1]
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		return f * mul
	}
	return 0
}

func anyFloat(v any) float64 { return parseSIRate(v) }

func parseCKPool(body []byte, fullUser string) (*PoolStats, error) {
	var r struct {
		LastShare int64 `json:"lastshare"`
		Shares    any   `json:"shares"`
		Best      any   `json:"bestshare"`
		Workers   int   `json:"workers"`
		Worker    []struct {
			Name      string `json:"workername"`
			H1m       any    `json:"hashrate1m"`
			H5m       any    `json:"hashrate5m"`
			H1hr      any    `json:"hashrate1hr"`
			LastShare int64  `json:"lastshare"`
			Shares    any    `json:"shares"`
			Best      any    `json:"bestshare"`
		} `json:"worker"`
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("unreadable reply")
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, errors.New("unreadable reply")
	}
	if _, ok := raw["lastshare"]; !ok {
		return nil, errors.New("not found")
	}
	ps := &PoolStats{H1m: parseSIRate(raw["hashrate1m"]), H5m: parseSIRate(raw["hashrate5m"]), H1hr: parseSIRate(raw["hashrate1hr"]),
		LastShare: r.LastShare, Shares: anyFloat(r.Shares), Best: anyFloat(r.Best), Workers: r.Workers}
	fu := strings.TrimSpace(fullUser)
	for _, w := range r.Worker {
		if strings.EqualFold(w.Name, fu) {
			ps.H1m, ps.H5m, ps.H1hr = parseSIRate(w.H1m), parseSIRate(w.H5m), parseSIRate(w.H1hr)
			ps.LastShare, ps.Shares, ps.Best, ps.Matched = w.LastShare, anyFloat(w.Shares), anyFloat(w.Best), true
			break
		}
	}
	return ps, nil
}

func parsePublicPool(body []byte, worker string) (*PoolStats, error) {
	var r struct {
		Best    any `json:"bestDifficulty"`
		Workers []struct {
			Name     string `json:"name"`
			Best     any    `json:"bestDifficulty"`
			HashRate any    `json:"hashRate"`
			LastSeen string `json:"lastSeen"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, errors.New("unreadable reply")
	}
	ps := &PoolStats{Best: anyFloat(r.Best)}
	agg := func(match bool) {
		for _, w := range r.Workers {
			if match && !strings.EqualFold(w.Name, worker) {
				continue
			}
			ps.Workers++
			h := anyFloat(w.HashRate)
			ps.H5m += h
			if t, err := time.Parse(time.RFC3339Nano, w.LastSeen); err == nil && t.Unix() > ps.LastShare {
				ps.LastShare = t.Unix()
			}
		}
	}
	if worker != "" {
		agg(true)
		ps.Matched = ps.Workers > 0
	}
	if ps.Workers == 0 {
		agg(false)
	}
	// public-pool reports one rolling hashrate per session.
	ps.H1m, ps.H1hr = ps.H5m, ps.H5m
	return ps, nil
}

// fetch reads one stats page. Errors are fixed strings with no URL in them.
func (g *Integrity) fetch(ctx context.Context, src integSource, fullUser, worker string) (*PoolStats, error) {
	u := src.url
	if g.Rewrite != nil {
		u = g.Rewrite(u)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("bad request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HashWatch/"+strings.TrimPrefix(g.Version, "v"))
	resp, err := g.Client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, errors.New("timed out")
		}
		return nil, errors.New("no connection")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == 404:
		return nil, errors.New("not found")
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if src.kind == "publicpool" {
		return parsePublicPool(body, worker)
	}
	return parseCKPool(body, fullUser)
}

// Expect marks a pool edit made from HashWatch so it's logged as such, not as unexpected.
func (g *Integrity) Expect(id string, idx int, poolURL, user string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.state(id)
	s.expect = append(s.expect, integExpect{idx: idx, url: poolHostPort(poolURL), userHash: userHash(user), until: g.Now().Add(24 * time.Hour)})
}

func (g *Integrity) state(id string) *integState {
	s := g.st[id]
	if s == nil {
		s = &integState{activeIdx: -1}
		g.loadFP(id, s)
		g.st[id] = s
	}
	return s
}

// The pool fingerprint (URLs + short user hashes, never the user) is kept so a
// change made while HashWatch was off is still noticed.
func (g *Integrity) fpPath(id string) string {
	return filepath.Join(g.dir, "integrity-"+safeID(id)+".json")
}

func safeID(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}

func (g *Integrity) loadFP(id string, s *integState) {
	if g.dir == "" {
		return
	}
	b, err := os.ReadFile(g.fpPath(id))
	if err != nil {
		return
	}
	var v struct {
		FP []string `json:"pools"`
	}
	if json.Unmarshal(b, &v) == nil && len(v.FP) > 0 {
		s.fp, s.haveFP = v.FP, true
	}
}

func (g *Integrity) saveFP(id string, fp []string) {
	if g.dir == "" {
		return
	}
	b, _ := json.Marshal(map[string]any{"pools": fp})
	_ = os.WriteFile(g.fpPath(id), b, 0o600)
}

func avgH5m(hist []model.Sample, now time.Time) float64 {
	var sum float64
	var n int
	for _, h := range hist {
		if h.T >= now.Unix()-3600 && h.H5m != nil {
			sum += *h.H5m
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func poolName(idx int, u string) string { return fmt.Sprintf("Pool %d (%s)", idx+1, poolHostPort(u)) }

// Observe is called after every miner poll.
func (g *Integrity) Observe(id, name string, snap *model.Snapshot, hist []model.Sample) {
	if g == nil || snap == nil || !snap.OK || len(snap.Pools) == 0 {
		return
	}
	now := g.Now()
	var fire []Alert
	g.mu.Lock()
	s := g.state(id)
	base := Alert{Miner: id, MinerName: name, Kind: integKind}
	mk := func(level, title, body string) {
		a := base
		a.Level, a.Title, a.Body = level, title, body
		fire = append(fire, a)
	}

	// 1. Pool settings (URL or username) changed.
	fp := make([]string, len(snap.Pools))
	for i, p := range snap.Pools {
		fp[i] = poolHostPort(p.URL) + "|" + userHash(p.User)
	}
	if !s.haveFP {
		s.fp, s.haveFP = fp, true
		g.saveFP(id, fp)
	} else if strings.Join(fp, ",") != strings.Join(s.fp, ",") {
		for i := 0; i < len(fp) || i < len(s.fp); i++ {
			var was, is string
			if i < len(s.fp) {
				was = s.fp[i]
			}
			if i < len(fp) {
				is = fp[i]
			}
			if was == is {
				continue
			}
			wURL, wUser, _ := strings.Cut(was, "|")
			nURL, nUser, _ := strings.Cut(is, "|")
			expected := false
			kept := s.expect[:0]
			for _, e := range s.expect {
				if now.After(e.until) {
					continue
				}
				if !expected && e.idx == i && e.url == nURL && (e.userHash == "" || e.userHash == nUser) {
					expected = true
					continue
				}
				kept = append(kept, e)
			}
			s.expect = kept
			var what []string
			if wURL != nURL {
				what = append(what, fmt.Sprintf("its address is now %s (was %s)", orDash(nURL), orDash(wURL)))
			}
			if wUser != nUser {
				what = append(what, "its username changed (not shown, for privacy)")
			}
			title := fmt.Sprintf("Pool %d settings changed on %s", i+1, name)
			if expected {
				mk("info", title, fmt.Sprintf("Pool %d: %s. The change was made from %s.", i+1, strings.Join(what, ", and "), AppName))
			} else {
				mk("bad", title, fmt.Sprintf("Pool %d: %s. If you didn't change it, check the miner's pool settings now: a changed address or username can send your hashrate to someone else.", i+1, strings.Join(what, ", and ")))
			}
		}
		s.fp = fp
		g.saveFP(id, fp)
	}

	// 2. Which pool is active (debounced over two polls).
	act := -1
	for i, p := range snap.Pools {
		if p.StratumActive && (p.Status == "" || strings.EqualFold(p.Status, "Alive")) {
			act = i
			break
		}
	}
	if act >= 0 {
		p := snap.Pools[act]
		key := fmt.Sprintf("%d|%s", act, fp[act])
		switch {
		case s.activeKey == key:
			s.pendKey, s.pendN = "", 0
		case s.activeKey == "": // first sighting after start
			s.activeIdx, s.activeKey, s.activeAt = act, key, now
			s.nextFetch = now.Add(integFirstAfter)
		default:
			if s.pendKey != key {
				s.pendKey, s.pendN = key, 0
			}
			s.pendN++
			if s.pendN >= 2 {
				prev, prevURL := s.activeIdx, s.activeURL
				s.activeIdx, s.activeKey, s.activeAt = act, key, now
				s.pendKey, s.pendN = "", 0
				s.stats, s.state, s.reason, s.badSince, s.badFired, s.fetchErr = nil, "", "", time.Time{}, false, ""
				s.nextFetch = now.Add(integFirstAfter)
				if prev != act {
					mk("warn", name+" switched pools", fmt.Sprintf("Now mining on %s, was %s. If you didn't expect a failover (or %s's backup-pool automation didn't do it), check your internet and the pool's status.",
						poolName(act, p.URL), poolName(prev, prevURL), AppName))
				}
			}
		}
		if s.activeKey == key {
			s.activeURL, s.activeUser = p.URL, p.User
			s.minerLast = p.LastShareUnix
			s.plain = poolScheme(p.URL)
			if p.WorkDifficulty != nil {
				s.workDiff = *p.WorkDifficulty
			}
		}
		// 3. Share difficulty far above any pool's maximum for a home miner.
		if wd := p.WorkDifficulty; wd != nil && *wd > integDiffMax {
			s.diffHigh++
			if s.diffHigh >= 2 && !s.diffFired {
				s.diffFired = true
				mk("bad", "Suspicious share difficulty on "+name, fmt.Sprintf("%s is being asked for share difficulty %s, far above the %s a pool sets for a home miner. Hijacked plain Stratum V1 connections have shown exactly this. Check the pool's stats page for your hashrate.",
					poolName(act, p.URL), fmtBig(*wd), fmtBig(integDiffMax)))
			}
		} else if wd != nil {
			if s.diffFired {
				mk("good", "Share difficulty back to normal on "+name, fmt.Sprintf("%s is at difficulty %s again.", poolName(act, p.URL), fmtBig(*wd)))
			}
			s.diffHigh, s.diffFired = 0, false
		}
	}
	if v := snap.Hashrate.THs5m; v != nil {
		s.minerH5m = *v * 1e12
	}
	s.minerH1h = avgH5m(hist, now) * 1e12

	// 4. Pool stats fetch (rate-limited, off with the alert toggle).
	var job func()
	if !g.on() {
		s.state, s.reason = "off", "Turned off in Alerts, so "+AppName+" isn't reading the pool's stats."
	} else if s.activeKey != "" && !s.fetching && !now.Before(s.nextFetch) {
		addr, worker, ok := payoutAddr(s.activeUser)
		srcs := []integSource(nil)
		if ok {
			srcs = statsSources(s.activeURL, addr)
		}
		if len(srcs) == 0 {
			s.state, s.reason, s.stats = "unsupported", poolHostOnly(s.activeURL)+" doesn't publish per-address stats "+AppName+" can read.", nil
			s.nextFetch = now.Add(integEvery)
		} else {
			s.fetching = true
			key, fullUser := s.activeKey, s.activeUser
			job = func() { g.runFetch(id, name, key, srcs, fullUser, worker) }
		}
	}
	g.mu.Unlock()
	for _, a := range fire {
		g.Alerts.Fire(a)
	}
	if job != nil {
		if g.syncFetch {
			job()
		} else {
			go job()
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func fmtBig(v float64) string {
	s := strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func fmtTH(h float64) string {
	th := h / 1e12
	switch {
	case th >= 10:
		return fmt.Sprintf("%.1f TH/s", th)
	case th >= 0.01:
		return fmt.Sprintf("%.2f TH/s", th)
	case h <= 0:
		return "0 TH/s"
	}
	return fmt.Sprintf("%.0f GH/s", h/1e9)
}

func ago(d time.Duration) string {
	switch {
	case d < 90*time.Second:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < 2*time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}

func (g *Integrity) runFetch(id, name, key string, srcs []integSource, fullUser, worker string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*integTimeout)
	defer cancel()
	var ps *PoolStats
	var used integSource
	var ferr error
	for _, src := range srcs {
		ps, ferr = g.fetch(ctx, src, fullUser, worker)
		if ferr == nil {
			used = src
			break
		}
	}
	now := g.Now()
	var fire []Alert
	g.mu.Lock()
	s := g.state(id)
	s.fetching = false
	if s.activeKey != key { // pool changed while fetching
		g.mu.Unlock()
		return
	}
	s.checkedAt = now
	if ferr != nil {
		s.fetchErr, s.stats, s.statsLabel = ferr.Error(), nil, "" // don't show stale numbers as current
		s.state, s.reason = "unknown", fmt.Sprintf("Couldn't read %s's stats (%s). Not counted as a problem; %s tries again later.", srcs[0].label, s.fetchErr, AppName)
		s.nextFetch = now.Add(integRetry)
		g.mu.Unlock()
		return
	}
	s.fetchErr, s.stats, s.statsLabel, s.statsLag = "", ps, used.label, used.lag
	s.nextFetch = now.Add(integEvery)
	fire = g.evaluate(s, id, name, now)
	g.mu.Unlock()
	for _, a := range fire {
		g.Alerts.Fire(a)
	}
}

// evaluate turns the latest pool stats + miner counters into a verdict. Locked.
func (g *Integrity) evaluate(s *integState, id, name string, now time.Time) []Alert {
	var out []Alert
	mk := func(level, title, body string) {
		out = append(out, Alert{Miner: id, MinerName: name, Kind: integKind, Level: level, Title: title, Body: body})
	}
	ps := s.stats
	minerH := s.minerH1h
	if minerH <= 0 {
		minerH = s.minerH5m
	}
	if s.minerH5m < 0.05e12 || s.minerLast == 0 || now.Sub(time.Unix(s.minerLast, 0)) > integMinerFresh {
		s.state, s.reason = "unknown", "The miner isn't hashing with accepted shares right now, so there's nothing to compare. The Offline and Pool alerts cover that."
		s.badSince = time.Time{}
		return out
	}
	stale := integStaleMin
	if s.workDiff > 0 && minerH > 0 {
		if exp := time.Duration(s.workDiff * 4294967296 / minerH * 10 * float64(time.Second)); exp > stale {
			stale = exp
		}
	}
	if s.statsLag {
		stale += integRawLag
	}
	onPool := now.Sub(s.activeAt)
	lastAge := time.Duration(math.MaxInt64)
	if ps.LastShare > 0 {
		lastAge = now.Sub(time.Unix(ps.LastShare, 0))
		if lastAge < 0 {
			lastAge = 0
		}
	}
	isStale := lastAge > stale
	isLow := onPool >= integLowAfter && s.minerH1h > 0 && ps.H1hr < integLowRatio*s.minerH1h
	src := s.statsLabel
	switch {
	case !isStale && !isLow:
		s.state = "ok"
		if onPool >= integLowAfter && s.minerH1h > 0 {
			s.reason = fmt.Sprintf("%s credits %s over the last hour (the miner reports %s). Last share it saw: %s ago.", src, fmtTH(ps.H1hr), fmtTH(s.minerH1h), ago(lastAge))
		} else {
			s.reason = fmt.Sprintf("%s saw this miner's last share %s ago. The hashrate comparison starts after %d min on this pool.", src, ago(lastAge), int(integLowAfter.Minutes()))
		}
		if s.badFired {
			mk("good", "Pool credit back to normal for "+name, fmt.Sprintf("%s is crediting the miner's work again.", src))
		}
		s.badSince, s.badFired = time.Time{}, false
	case onPool < stale+integStaleMin/4 && !isLow:
		s.state = "waiting"
		s.reason = fmt.Sprintf("The miner joined this pool %s ago and %s hasn't caught up yet. Pool stats lag, so %s gives it %s before judging.", ago(onPool), src, AppName, ago(stale+integStaleMin/4))
	default:
		s.state = "mismatch"
		var why []string
		if isStale {
			if ps.LastShare == 0 {
				why = append(why, "shows no share from this miner")
			} else {
				why = append(why, "last saw a share "+ago(lastAge)+" ago")
			}
		}
		if isLow {
			why = append(why, fmt.Sprintf("credits only %s over the last hour", fmtTH(ps.H1hr)))
		}
		if s.badSince.IsZero() {
			s.badSince = now
		}
		s.reason = fmt.Sprintf("The miner reports %s with accepted shares (last %s ago), but %s %s.", fmtTH(minerH), ago(now.Sub(time.Unix(s.minerLast, 0))), src, strings.Join(why, " and "))
		if !s.badFired && now.Sub(s.badSince) >= integSustain {
			s.badFired = true
			mk("bad", "Pool isn't crediting "+name+"'s work", fmt.Sprintf("For %s the miner has reported %s with accepted shares, but %s %s. That's what a hijacked plain Stratum V1 connection looks like (the miner keeps hashing for someone else). Check the pool's stats page, and consider a TLS or Stratum V2 route.",
				ago(now.Sub(s.badSince)), fmtTH(minerH), src, strings.Join(why, " and ")))
		}
	}
	return out
}

// View is the dashboard payload. It never contains the payout address.
func (g *Integrity) View(id string) map[string]any {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.st[id]
	if s == nil {
		return map[string]any{"state": "waiting", "reason": "Waiting for the first reading from the miner.", "on": g.on()}
	}
	state, reason := s.state, s.reason
	if !g.on() {
		state, reason = "off", "Turned off in Alerts, so "+AppName+" isn't reading the pool's stats."
	} else if state == "" {
		state, reason = "waiting", "First check of the pool's public stats is coming up."
	}
	v := map[string]any{"on": g.on(), "state": state, "reason": reason, "scheme": s.plain,
		"window_min": int(integSustain.Minutes()), "every_sec": int(integEvery.Seconds())}
	if s.activeIdx >= 0 && s.activeURL != "" {
		v["pool"] = fmt.Sprintf("Pool %d", s.activeIdx+1)
		v["pool_host"] = poolHostPort(s.activeURL)
	}
	if !s.checkedAt.IsZero() {
		v["checked_unix"] = s.checkedAt.Unix()
	}
	if !s.nextFetch.IsZero() && g.on() {
		v["next_unix"] = s.nextFetch.Unix()
	}
	if !s.badSince.IsZero() {
		v["bad_since_unix"] = s.badSince.Unix()
	}
	if s.statsLabel != "" {
		v["source"] = s.statsLabel
	}
	if ps := s.stats; ps != nil {
		v["credited"] = map[string]any{"ths_1m": ps.H1m / 1e12, "ths_5m": ps.H5m / 1e12, "ths_1h": ps.H1hr / 1e12,
			"last_share_unix": ps.LastShare, "shares": ps.Shares, "best": ps.Best, "workers": ps.Workers, "worker_match": ps.Matched}
	}
	v["miner"] = map[string]any{"ths_5m": s.minerH5m / 1e12, "ths_1h": s.minerH1h / 1e12, "last_share_unix": s.minerLast}
	return v
}
