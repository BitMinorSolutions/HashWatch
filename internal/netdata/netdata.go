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
	"strings"
	"sync"
	"time"
)

// DefaultBase is the public mempool.space API.
const DefaultBase = "https://mempool.space/api"

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
}

// Fetcher keeps Snapshot fresh in the background.
type Fetcher struct {
	Base       string // mempool API base
	PriceURL   string // defaults to Base + "/v1/prices"
	Client     *http.Client
	Path       string        // cache file
	Every      time.Duration // network refresh (default 2 min)
	PriceEvery time.Duration // price refresh (default 10 min)
	// Enabled reports which sources may be contacted right now (privacy settings).
	Enabled func() (network, price bool)
	Now     func() time.Time

	mu   sync.Mutex
	snap Snapshot
	kick chan struct{}
}

func New(dir string) *Fetcher {
	f := &Fetcher{Base: DefaultBase, Path: filepath.Join(dir, "netcache.json"), Every: 2 * time.Minute, PriceEvery: 10 * time.Minute,
		Client: &http.Client{Timeout: 12 * time.Second}, Now: time.Now, kick: make(chan struct{}, 1)}
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

// Run refreshes until ctx is done.
func (f *Fetcher) Run(ctx context.Context) {
	var lastNet, lastPrice time.Time
	for {
		net, price := true, true
		if f.Enabled != nil {
			net, price = f.Enabled()
		}
		now := f.Now()
		if net && now.Sub(lastNet) >= f.Every {
			f.RefreshNetwork(ctx)
			lastNet = now
		}
		if price && now.Sub(lastPrice) >= f.PriceEvery {
			f.RefreshPrice(ctx)
			lastPrice = now
		}
		t := time.NewTimer(20 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-f.kick:
			lastNet, lastPrice = time.Time{}, time.Time{}
		case <-t.C:
		}
	}
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

// RefreshNetwork fetches recent blocks (which carry difficulty and the tip height).
func (f *Fetcher) RefreshNetwork(ctx context.Context) error {
	var raw []mpBlock
	err := f.get(ctx, strings.TrimRight(f.Base, "/")+"/v1/blocks", &raw)
	if err == nil && len(raw) == 0 {
		err = errors.New("no blocks in reply")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.snap.LastErr = err.Error()
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
	f.saveLocked()
	return nil
}

// RefreshPrice fetches BTC/USD.
func (f *Fetcher) RefreshPrice(ctx context.Context) error {
	u := f.PriceURL
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.snap.PriceErr = err.Error()
		return err
	}
	f.snap.PriceUSD, f.snap.PriceFetched, f.snap.PriceErr = p.USD, f.Now().Unix(), ""
	f.snap.PriceSource = "mempool.space"
	f.saveLocked()
	return nil
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
