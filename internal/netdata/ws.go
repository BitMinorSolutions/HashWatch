package netdata

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// runWS maintains a connection to mempool.space (or WSURL) and applies live
// blocks, difficulty-adjustment, and USD price (conversions) the moment they
// arrive. It reconnects with exponential backoff. When the socket is down,
// Run's REST loop covers price and difficulty.
func (f *Fetcher) runWS(ctx context.Context) {
	if !f.wsEnabled() {
		return
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		netOn, priceOn := true, true
		if f.Enabled != nil {
			netOn, priceOn = f.Enabled()
		}
		if !netOn && !priceOn {
			f.setWSConnected(false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Second):
			}
			continue
		}
		err := f.wsSession(ctx)
		f.setWSConnected(false)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("netdata websocket: %v (retry in %s)", err, backoff)
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if backoff < time.Minute {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
		// Reset backoff after a later successful session (handled inside wsSession).
		if f.wsHealthy() {
			backoff = time.Second
		}
	}
}

func (f *Fetcher) wsEnabled() bool {
	if f.WSURL == "-" {
		return false
	}
	if f.WSURL != "" {
		return true
	}
	return f.usesDefaultBase()
}

func (f *Fetcher) wsURL() string {
	if f.WSURL != "" && f.WSURL != "-" {
		return f.WSURL
	}
	return DefaultWSURL
}

func (f *Fetcher) wsSession(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{
		HTTPClient: f.wsHTTPClient(),
		HTTPHeader: http.Header{"User-Agent": []string{"HashWatch (solo miner dashboard)"}},
	}
	conn, _, err := websocket.Dial(dialCtx, f.wsURL(), opts)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(2 << 20)

	if err := writeWS(ctx, conn, map[string]any{"action": "init"}); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if err := writeWS(ctx, conn, map[string]any{"action": "want", "data": []string{"blocks", "stats"}}); err != nil {
		return fmt.Errorf("want: %w", err)
	}

	f.setWSConnected(true)
	// Protocol pings detect a silently dead socket well before the idle
	// read timeout, so REST can take over instead of waiting for a block.
	go func() {
		tick := time.NewTicker(wsPingEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					_ = conn.Close(websocket.StatusGoingAway, "ping failed")
					return
				}
			}
		}
	}()
	// A quiet socket for this long is treated as dead so we reconnect;
	// REST already resumes earlier if NetFetched ages out.
	const idle = 15 * time.Minute
	for {
		readCtx, cancelRead := context.WithTimeout(ctx, idle)
		_, data, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			return err
		}
		if len(data) == 0 || string(data) == "{}" || string(data) == "[]" {
			continue
		}
		if err := f.applyWSMessage(data); err != nil {
			log.Printf("netdata websocket: drop message: %v", err)
		}
	}
}

func (f *Fetcher) wsHTTPClient() *http.Client {
	if f.Client != nil {
		// The regular REST client has a 12s timeout that would kill a long-lived
		// websocket; clone it without a timeout.
		c := *f.Client
		c.Timeout = 0
		return &c
	}
	return &http.Client{}
}

func writeWS(ctx context.Context, conn *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}

// applyWSMessage updates the snapshot from a mempool.space websocket payload.
// Unknown fields are ignored. Returns an error only when the JSON itself is unusable.
func (f *Fetcher) applyWSMessage(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	now := f.Now().Unix()
	changed := false

	f.mu.Lock()
	if msg, ok := raw["blocks"]; ok {
		var list []mpBlock
		if err := json.Unmarshal(msg, &list); err == nil && len(list) > 0 {
			if f.applyBlocksLocked(list, now) {
				changed = true
			} else {
				f.snap.NetFetched = now
			}
		}
	}
	if msg, ok := raw["block"]; ok {
		var one mpBlock
		if err := json.Unmarshal(msg, &one); err == nil && one.Height > 0 {
			if f.applyBlocksLocked([]mpBlock{one}, now) {
				changed = true
			} else {
				f.snap.NetFetched = now
			}
		}
	}
	if msg, ok := raw["conversions"]; ok {
		if usd, ok := usdFromConversions(msg); ok {
			if f.snap.PriceUSD != usd {
				f.snap.PriceUSD = usd
				changed = true
			}
			f.snap.PriceFetched = now
			f.snap.PriceSource = "mempool.space"
			f.snap.PriceErr = ""
		}
	}
	if msg, ok := raw["da"]; ok && string(msg) != "null" {
		if da, ok := parseDiffAdj(msg); ok {
			if !diffAdjEqual(f.snap.DiffAdj, da) {
				f.snap.DiffAdj = da
				changed = true
			}
			// Any successful stats receipt is a freshness heartbeat, even
			// when difficulty-adjustment fields have not materially changed.
			f.snap.NetFetched = now
		}
	}
	// Other mempool "stats" fields (fees, mempoolInfo) are also a successful
	// network receipt, even when they do not change a UI value.
	if _, ok := raw["mempoolInfo"]; ok {
		f.snap.NetFetched = now
	}
	if _, ok := raw["fees"]; ok {
		f.snap.NetFetched = now
	}
	// Always mark the socket healthy when we parsed a known payload,
	// but only bump Rev / wake waiters when a value the UI shows changed.
	if len(raw) > 0 {
		f.snap.WSLastEvent = now
		f.snap.WSConnected = true
	}
	if changed {
		f.bumpLocked()
		f.saveLocked()
	}
	f.mu.Unlock()
	if changed {
		f.notify()
	}
	return nil
}

func (f *Fetcher) applyBlocksLocked(raw []mpBlock, now int64) bool {
	// Merge: keep the newest tip, prepend any strictly newer heights, and
	// refresh fields of blocks we already have.
	prevTip, prevDiff := f.snap.TipHeight, f.snap.Difficulty
	have := map[int64]int{}
	for i, b := range f.snap.Blocks {
		have[b.Height] = i
	}
	var newest []Block
	for _, b := range raw {
		if b.Height <= 0 {
			continue
		}
		nb := blockFromMP(b)
		if i, ok := have[nb.Height]; ok {
			f.snap.Blocks[i] = nb
			continue
		}
		newest = append(newest, nb)
	}
	if len(newest) > 0 {
		f.snap.Blocks = append(newest, f.snap.Blocks...)
		if len(f.snap.Blocks) > 24 {
			f.snap.Blocks = f.snap.Blocks[:24]
		}
	}
	if len(f.snap.Blocks) == 0 {
		return false
	}
	// Tip is the highest height we know.
	tip := f.snap.Blocks[0]
	for _, b := range f.snap.Blocks[1:] {
		if b.Height > tip.Height {
			tip = b
		}
	}
	f.snap.TipHeight = tip.Height
	if tip.Diff > 0 {
		f.snap.Difficulty = tip.Diff
	}
	var fees int64
	n := 0
	for _, b := range f.snap.Blocks {
		if b.FeesSat > 0 {
			fees += b.FeesSat
			n++
		}
	}
	if n > 0 {
		f.snap.AvgFeesSat = fees / int64(n)
	}
	f.snap.NetFetched = now
	f.snap.Source = "mempool.space"
	f.snap.LastErr = ""
	return len(newest) > 0 || f.snap.TipHeight != prevTip || f.snap.Difficulty != prevDiff
}

func diffAdjEqual(a, b *DiffAdj) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Progress percent ticks often; only wake the UI on a material retarget change.
	dc := a.DifficultyChange - b.DifficultyChange
	if dc < 0 {
		dc = -dc
	}
	return a.RemainingBlocks == b.RemainingBlocks && a.NextRetargetHeight == b.NextRetargetHeight &&
		a.PreviousRetarget == b.PreviousRetarget && dc < 0.001
}

func blockFromMP(b mpBlock) Block {
	nb := Block{Height: b.Height, Time: b.Timestamp, RewardSat: b.Extras.Reward, FeesSat: b.Extras.TotalFees, TxCount: b.TxCount, Diff: b.Diff}
	if b.Extras.Pool != nil {
		nb.Pool, nb.PoolSlug = b.Extras.Pool.Name, b.Extras.Pool.Slug
	}
	nb.Solo, nb.Unknown = Classify(nb.Pool, nb.PoolSlug)
	if nb.Unknown && nb.Pool == "" {
		nb.Pool = "Unknown"
	}
	return nb
}

func usdFromConversions(raw json.RawMessage) (float64, bool) {
	// Usual shape: {"time":..., "USD": 97000, "EUR": ...}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	for _, k := range []string{"USD", "usd"} {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				if n > 0 {
					return n, true
				}
			case json.Number:
				f, err := n.Float64()
				if err == nil && f > 0 {
					return f, true
				}
			}
		}
	}
	return 0, false
}

func parseDiffAdj(raw json.RawMessage) (*DiffAdj, bool) {
	var src struct {
		ProgressPercent       float64 `json:"progressPercent"`
		DifficultyChange      float64 `json:"difficultyChange"`
		EstimatedRetargetDate int64   `json:"estimatedRetargetDate"`
		RemainingBlocks       int64   `json:"remainingBlocks"`
		NextRetargetHeight    int64   `json:"nextRetargetHeight"`
		PreviousRetarget      float64 `json:"previousRetarget"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, false
	}
	da := &DiffAdj{
		ProgressPercent:    src.ProgressPercent,
		DifficultyChange:   src.DifficultyChange,
		RemainingBlocks:    src.RemainingBlocks,
		NextRetargetHeight: src.NextRetargetHeight,
		PreviousRetarget:   src.PreviousRetarget,
	}
	// mempool sends the retarget date in milliseconds.
	if src.EstimatedRetargetDate > 1e12 {
		da.EstimatedRetargetDate = src.EstimatedRetargetDate / 1000
	} else if src.EstimatedRetargetDate > 0 {
		da.EstimatedRetargetDate = src.EstimatedRetargetDate
	}
	return da, true
}
