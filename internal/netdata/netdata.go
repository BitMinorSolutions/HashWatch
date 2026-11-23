// Package netdata fetches public Bitcoin network data for the lottery card:
// difficulty, tip height and recent blocks (with the pool/miner tag) from
// mempool.space, and the BTC/USD price. Every call is optional: the caller
// switches each source on or off (privacy), results are cached on disk so the
// app still shows the last known values offline, and every value carries the
// time it was fetched and where from, so the UI can label it honestly.
package netdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBase is the public mempool.space API.
const DefaultBase = "https://mempool.space/api"

// Fallback sources used only when the primary mempool.space fetch fails.
// Tests that point Base or PriceURL at a stub server never reach these.
var (
	FallbackDifficultyURL = "https://blockchain.info/q/getdifficulty"
	FallbackPriceURL      = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"
)

// Default refresh intervals. Network stays at 2 minutes; price is 60s.
// On HTTP 429 or any other fetch error the wait doubles, up to MaxBackoff,
// then returns to these on the next success. Last good values are kept.
const (
	DefaultEvery      = 2 * time.Minute
	DefaultPriceEvery = 60 * time.Second
	MaxBackoff        = 5 * time.Minute
)

// Block is one recent network block.
type Block struct {
	Height    int64   `json:"height"`
	Time      int64   `json:"time"`
	Pool      string  `json:"pool"`
	PoolSlug  string  `json:"pool_slug"`
	Solo      bool    `json:"solo"`    // tagged as a solo/lottery miner (e.g. Solo CK, Public Pool)
	Unknown   bool    `json:"unknown"` // no pool tag at all (could be anyone, including a solo miner)
	RewardSat int64   `json:"reward_sat"`
	FeesSat   int64   `json:"fees_sat"`
	TxCount   int     `json:"tx_count"`
	Diff      float64 `json:"difficulty"`
}

// Snapshot is the cached network state.
type Snapshot struct {
	Difficulty   float64 `json:"difficulty"`
	TipHeight    int64   `json:"tip_height"`
	Blocks       []Block `json:"blocks"`
	AvgFeesSat   int64   `json:"avg_fees_sat"`
	NetFetched   int64   `json:"net_fetched"` // unix time difficulty/blocks were fetched
	PriceUSD     float64 `json:"price_usd"`
	PriceFetched int64   `json:"price_fetched"`
	Source       string  `json:"source"`
	PriceSource  string  `json:"price_source"`
	LastErr      string  `json:"last_err,omitempty"`
	PriceErr     string  `json:"price_err,omitempty"`
	// Live websocket (mempool.space). WSConnected is true while the socket is up.
	WSConnected bool    `json:"ws_connected"`
	WSLastEvent int64   `json:"ws_last_event,omitempty"` // unix time of the last applied ws message
	DiffAdj     *DiffAdj `json:"diff_adj,omitempty"`
	// Rev increments on every applied network/price change so the UI can wait for it.
	Rev int64 `json:"rev,omitempty"`
}

// DiffAdj is Bitcoin's current difficulty-adjustment progress, from mempool.space.
type DiffAdj struct {
	ProgressPercent       float64 `json:"progress_percent"`
	DifficultyChange      float64 `json:"difficulty_change"` // estimated % change at retarget
	EstimatedRetargetDate int64   `json:"estimated_retarget_date,omitempty"` // unix seconds
	RemainingBlocks       int64   `json:"remaining_blocks"`
	NextRetargetHeight    int64   `json:"next_retarget_height,omitempty"`
	PreviousRetarget      float64 `json:"previous_retarget,omitempty"`
}

// Fetcher keeps Snapshot fresh in the background.
type Fetcher struct {
	Base          string // mempool API base
	PriceURL      string // defaults to Base + "/v1/prices"
	Client        *http.Client
	Path          string        // cache file
	Every         time.Duration // network refresh (default 2 min)
	PriceEvery    time.Duration // price refresh (default 60s)
	DiffFallback  string        // override FallbackDifficultyURL (tests)
	PriceFallback string        // override FallbackPriceURL (tests)
	// Enabled reports which sources may be contacted right now (privacy settings).
	Enabled func() (network, price bool)
	Now     func() time.Time
	// OnChange is called (without the fetcher lock held) after the snapshot changes.
	OnChange func(Snapshot)
	// WSURL overrides the default mempool websocket (tests). Empty = DefaultWSURL
	// when using the default mempool.space API; a literal "-" disables the socket.
	WSURL string

	mu     sync.Mutex
	snap   Snapshot
	kick   chan struct{}
	wait   chan struct{}
	wsUp   bool
}

func New(dir string) *Fetcher {
	f := &Fetcher{Base: DefaultBase, Path: filepath.Join(dir, "netcache.json"), Every: DefaultEvery, PriceEvery: DefaultPriceEvery,
		Client: &http.Client{Timeout: 12 * time.Second}, Now: time.Now, kick: make(chan struct{}, 1), wait: make(chan struct{})}
	if b, err := os.ReadFile(f.Path); err == nil {
		_ = json.Unmarshal(b, &f.snap)
	}
	return f
}

// Get returns the cached snapshot.
func (f *Fetcher) Get() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.snap
	s.Blocks = append([]Block(nil), f.snap.Blocks...)
	return s
}

// Kick asks the background loop to refresh now (e.g. after privacy settings change).
func (f *Fetcher) Kick() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// nextBackoff doubles cur, capped at max. cur should already be the last wait
// (normally the default interval), so the first error waits twice as long.
func nextBackoff(cur, max time.Duration) time.Duration {
	if max <= 0 {
		max = MaxBackoff
	}
	if cur <= 0 {
		cur = time.Second
	}
	next := cur * 2
	if next > max || next <= cur { // cap, and guard overflow
		return max
	}
	return next
}

func (f *Fetcher) netEvery() time.Duration {
	if f.Every > 0 {
		return f.Every
	}
	return DefaultEvery
}

func (f *Fetcher) priceEvery() time.Duration {
	if f.PriceEvery > 0 {
		return f.PriceEvery
	}
	return DefaultPriceEvery
}

// Run refreshes until ctx is done. Failed fetches (including HTTP 429) keep
// the last good values and their timestamps, and double the wait up to
// MaxBackoff. A successful fetch, or Kick, resets the wait to the default.
func (f *Fetcher) Run(ctx context.Context) {
	go f.runWS(ctx)
	var lastNet, lastPrice time.Time
	netWait, priceWait := f.netEvery(), f.priceEvery()
	for {
		net, price := true, true
		if f.Enabled != nil {
			net, price = f.Enabled()
		}
		now := f.Now()
		// When the websocket is healthy, skip REST polling; fall back to it the
		// moment the socket is down or has gone quiet.
		wsLive := f.wsHealthy()
		if net && !wsLive && (lastNet.IsZero() || now.Sub(lastNet) >= netWait) {
			if err := f.RefreshNetwork(ctx); err != nil {
				netWait = nextBackoff(netWait, MaxBackoff)
			} else {
				netWait = f.netEvery()
			}
			lastNet = now
		}
		if price && !wsLive && (lastPrice.IsZero() || now.Sub(lastPrice) >= priceWait) {
			if err := f.RefreshPrice(ctx); err != nil {
				priceWait = nextBackoff(priceWait, MaxBackoff)
			} else {
				priceWait = f.priceEvery()
			}
			lastPrice = now
		}
		wait := timeUntil(now, net, lastNet, netWait, price, lastPrice, priceWait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-f.kick:
			t.Stop()
			lastNet, lastPrice = time.Time{}, time.Time{}
			netWait, priceWait = f.netEvery(), f.priceEvery()
		case <-t.C:
		}
	}
}

// timeUntil is how long Run should sleep before the next due refresh.
func timeUntil(now time.Time, net bool, lastNet time.Time, netWait time.Duration, price bool, lastPrice time.Time, priceWait time.Duration) time.Duration {
	wait := MaxBackoff
	due := func(on bool, last time.Time, interval time.Duration) {
		if !on {
			return
		}
		var d time.Duration
		if last.IsZero() {
			d = 0
		} else {
			d = interval - now.Sub(last)
		}
		if d < wait {
			wait = d
		}
	}
	due(net, lastNet, netWait)
	due(price, lastPrice, priceWait)
	if !net && !price {
		return 20 * time.Second
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func (f *Fetcher) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "HashWatch (solo miner dashboard)")
	res, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
}

type mpBlock struct {
	ID        string  `json:"id"`
	Height    int64   `json:"height"`
	Timestamp int64   `json:"timestamp"`
	TxCount   int     `json:"tx_count"`
	Diff      float64 `json:"difficulty"`
	Extras    struct {
		Reward    int64 `json:"reward"`
		TotalFees int64 `json:"totalFees"`
		Pool      *struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"pool"`
	} `json:"extras"`
}

// soloRe matches pool tags that are solo / lottery mining services.
var soloRe = regexp.MustCompile(`(?i)\bsolo\b|solock|solo ?ck|public ?pool|publicpool|ckpool solo|solo\.ckpool`)

// Classify marks a block's pool tag as a solo miner or unknown.
func Classify(name, slug string) (solo, unknown bool) {
	if name == "" && slug == "" || strings.EqualFold(slug, "unknown") || strings.EqualFold(name, "unknown") {
		return false, true
	}
	return soloRe.MatchString(name) || soloRe.MatchString(slug), false
}

func (f *Fetcher) usesDefaultBase() bool {
	b := f.Base
	return b == "" || b == DefaultBase
}

// RefreshNetwork fetches recent blocks (which carry difficulty and the tip height).
// If that fails and we are on the default mempool.space API, it falls back to
// blockchain.info for the current difficulty so the odds card can stay current.
func (f *Fetcher) RefreshNetwork(ctx context.Context) error {
	var raw []mpBlock
	err := f.get(ctx, strings.TrimRight(f.Base, "/")+"/v1/blocks", &raw)
	if err == nil && len(raw) == 0 {
		err = errors.New("no blocks in reply")
	}
	if err != nil && f.difficultyFallbackURL() != "" {
		if ferr := f.refreshDifficultyFallback(ctx); ferr == nil {
			return nil
		}
	}
	f.mu.Lock()
	if err != nil {
		f.snap.LastErr = err.Error()
		f.mu.Unlock()
		return err
	}
	var blocks []Block
	var fees int64
	for _, b := range raw {
		nb := Block{Height: b.Height, Time: b.Timestamp, RewardSat: b.Extras.Reward, FeesSat: b.Extras.TotalFees, TxCount: b.TxCount, Diff: b.Diff}
		if b.Extras.Pool != nil {
			nb.Pool, nb.PoolSlug = b.Extras.Pool.Name, b.Extras.Pool.Slug
		}
		nb.Solo, nb.Unknown = Classify(nb.Pool, nb.PoolSlug)
		if nb.Unknown && nb.Pool == "" {
			nb.Pool = "Unknown"
		}
		blocks = append(blocks, nb)
		fees += b.Extras.TotalFees
	}
	f.snap.Blocks = blocks
	f.snap.TipHeight = raw[0].Height
	f.snap.Difficulty = raw[0].Diff
	f.snap.AvgFeesSat = fees / int64(len(raw))
	f.snap.NetFetched = f.Now().Unix()
	f.snap.Source = "mempool.space"
	f.snap.LastErr = ""
	f.bumpLocked()
	f.saveLocked()
	f.mu.Unlock()
	f.notify()
	return nil
}

func (f *Fetcher) difficultyFallbackURL() string {
	if f.DiffFallback != "" {
		return f.DiffFallback
	}
	if f.usesDefaultBase() {
		return FallbackDifficultyURL
	}
	return ""
}

func (f *Fetcher) priceFallbackURL() string {
	if f.PriceFallback != "" {
		return f.PriceFallback
	}
	if f.PriceURL == "" && f.usesDefaultBase() {
		return FallbackPriceURL
	}
	return ""
}

func (f *Fetcher) refreshDifficultyFallback(ctx context.Context) error {
	u := f.difficultyFallbackURL()
	if u == "" {
		return errors.New("no difficulty fallback")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "HashWatch (solo miner dashboard)")
	res, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", u, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return err
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil || d <= 0 {
		return fmt.Errorf("difficulty fallback: bad value %q", strings.TrimSpace(string(b)))
	}
	f.mu.Lock()
	f.snap.Difficulty = d
	f.snap.NetFetched = f.Now().Unix()
	f.snap.Source = "blockchain.info"
	f.snap.LastErr = ""
	f.bumpLocked()
	f.saveLocked()
	f.mu.Unlock()
	f.notify()
	return nil
}

// RefreshPrice fetches BTC/USD from mempool.space, then CoinGecko if that fails
// and we are on the default sources (so tests with a stub PriceURL stay isolated).
func (f *Fetcher) RefreshPrice(ctx context.Context) error {
	u := f.PriceURL
	src := "mempool.space"
	if u == "" {
		u = strings.TrimRight(f.Base, "/") + "/v1/prices"
	}
	var p struct {
		USD float64 `json:"USD"`
	}
	err := f.get(ctx, u, &p)
	if err == nil && p.USD <= 0 {
		err = errors.New("no USD price in reply")
	}
	if err != nil && f.priceFallbackURL() != "" {
		if usd, ferr := f.fetchCoinGeckoPrice(ctx); ferr == nil {
			p.USD, err, src = usd, nil, "coingecko"
		}
	}
	f.mu.Lock()
	if err != nil {
		f.snap.PriceErr = err.Error()
		f.mu.Unlock()
		return err
	}
	f.snap.PriceUSD, f.snap.PriceFetched, f.snap.PriceErr = p.USD, f.Now().Unix(), ""
	f.snap.PriceSource = src
	f.bumpLocked()
	f.saveLocked()
	f.mu.Unlock()
	f.notify()
	return nil
}

func (f *Fetcher) fetchCoinGeckoPrice(ctx context.Context) (float64, error) {
	var raw map[string]map[string]float64
	if err := f.get(ctx, f.priceFallbackURL(), &raw); err != nil {
		return 0, err
	}
	usd := raw["bitcoin"]["usd"]
	if usd <= 0 {
		return 0, errors.New("no USD price in CoinGecko reply")
	}
	return usd, nil
}

func (f *Fetcher) saveLocked() {
	if f.Path == "" {
		return
	}
	b, _ := json.Marshal(f.snap)
	tmp := f.Path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, f.Path)
	}
}

// Set replaces the snapshot (tests and the simulator).
func (f *Fetcher) Set(s Snapshot) {
	f.mu.Lock()
	f.snap = s
	f.mu.Unlock()
}

// DefaultWSURL is mempool.space's public live websocket.
const DefaultWSURL = "wss://mempool.space/api/v1/ws"

// How long the socket may stay silent before we treat it as down and resume REST polling.
// A new block is ~10 minutes; conversions often arrive more often. 12 minutes covers a
// missed block with a little slack.
const wsQuietAfter = 12 * time.Minute

func (f *Fetcher) wsHealthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.wsUp && !f.snap.WSConnected {
		return false
	}
	last := f.snap.WSLastEvent
	if last <= 0 {
		// Just connected, waiting for the first message: treat as healthy briefly
		// so we don't immediately stampede REST, but not forever.
		return f.wsUp
	}
	return f.Now().Unix()-last < int64(wsQuietAfter/time.Second)
}

// Wait waits until the snapshot revision is greater than since, or ctx is done.
func (f *Fetcher) Wait(ctx context.Context, since int64) (Snapshot, int64) {
	for {
		f.mu.Lock()
		rev := f.snap.Rev
		ch := f.wait
		if ch == nil {
			ch = make(chan struct{})
			f.wait = ch
		}
		if rev > since {
			s := f.snap
			s.Blocks = append([]Block(nil), f.snap.Blocks...)
			if f.snap.DiffAdj != nil {
				d := *f.snap.DiffAdj
				s.DiffAdj = &d
			}
			f.mu.Unlock()
			return s, rev
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			s := f.Get()
			return s, s.Rev
		case <-ch:
		}
	}
}

func (f *Fetcher) bumpLocked() {
	f.snap.Rev++
	if f.wait != nil {
		close(f.wait)
	}
	f.wait = make(chan struct{})
}

func (f *Fetcher) notify() {
	if f.OnChange == nil {
		return
	}
	s := f.Get()
	f.OnChange(s)
}

func (f *Fetcher) setWSConnected(up bool) {
	f.mu.Lock()
	changed := f.wsUp != up || f.snap.WSConnected != up
	f.wsUp = up
	f.snap.WSConnected = up
	if changed {
		f.bumpLocked()
	}
	f.mu.Unlock()
	if changed {
		f.notify()
	}
}
