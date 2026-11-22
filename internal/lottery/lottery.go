// Package lottery is the solo-mining ("lottery ticket") math HashWatch shows.
//
// Every number here is an expectation from two inputs: your hashrate H (hashes per
// second) and the current network difficulty D. Bitcoin's proof of work means a
// single hash meets the network target with probability 1 / (D × 2^32), so:
//
//	expected hashes per block  = D × 2^32
//	expected time to a block   = D × 2^32 / H                    (seconds)
//	expected blocks in t secs  λ = H × t / (D × 2^32)
//	chance of ≥1 block in t    = 1 − e^(−λ)                      (Poisson)
//	chance per network block   = 1 − e^(−H × 600 / (D × 2^32))   (one 10-minute block interval)
//	"closest to a block"       = best share difficulty / D       (a share of difficulty D would be a block)
//
// Luck has no memory: these are long-run averages, not a countdown.
package lottery

import (
	"math"
)

// TwoPow32 is 2^32: difficulty 1 corresponds to 2^32 expected hashes.
const TwoPow32 = 4294967296.0

// BlockInterval is Bitcoin's target block time in seconds.
const BlockInterval = 600.0

const (
	Day  = 86400.0
	Year = 365.25 * Day
)

// HashesPerBlock is the expected number of hashes to find one block at difficulty d.
func HashesPerBlock(d float64) float64 { return d * TwoPow32 }

// Lambda is the expected number of blocks found with hashrate h (H/s) over t seconds.
func Lambda(h, d, t float64) float64 {
	if h <= 0 || d <= 0 || t <= 0 {
		return 0
	}
	return h * t / HashesPerBlock(d)
}

// Chance is the probability of finding at least one block in t seconds:
// 1 − (1 − p)^n, with p = 1/(D × 2^32) per hash and n = H × t hashes.
// Computed as -expm1(n × log1p(-p)) so tiny probabilities keep full precision.
// This is the right form over long periods (a year, many blocks); a linear
// sum n×p would exceed 1 and is only the expected count (see Lambda).
func Chance(h, d, t float64) float64 {
	if h <= 0 || d <= 0 || t <= 0 {
		return 0
	}
	p := 1 / HashesPerBlock(d)
	if p >= 1 {
		return 1
	}
	n := h * t
	return -math.Expm1(n * math.Log1p(-p))
}

// SecondsPerBlock is the expected (mean) time to one block, in seconds. 0 if unknown.
func SecondsPerBlock(h, d float64) float64 {
	if h <= 0 || d <= 0 {
		return 0
	}
	return HashesPerBlock(d) / h
}

// NetworkHashrate estimates the whole network's hashrate (H/s) from difficulty.
func NetworkHashrate(d float64) float64 { return HashesPerBlock(d) / BlockInterval }

// Closeness is best-share difficulty as a fraction of network difficulty (1.0 = a block).
func Closeness(bestShare, d float64) float64 {
	if bestShare <= 0 || d <= 0 {
		return 0
	}
	return bestShare / d
}

// Subsidy is the block subsidy in satoshis at a given height (50 BTC halving every 210,000 blocks).
func Subsidy(height int64) int64 {
	if height < 0 {
		return 0
	}
	halvings := height / 210000
	if halvings >= 64 {
		return 0
	}
	return (50 * 100_000_000) >> uint(halvings)
}

// OneIn turns a probability into "1 in N" (0 when p is 0).
func OneIn(p float64) float64 {
	if p <= 0 {
		return 0
	}
	return 1 / p
}

// Odds is everything the lottery card shows, computed in one place so the
// server, the tests and the UI all agree.
type Odds struct {
	HashrateHs      float64 `json:"hashrate_hs"`
	Difficulty      float64 `json:"difficulty"`
	HashesPerBlock  float64 `json:"hashes_per_block"`
	NetworkHs       float64 `json:"network_hs"`
	Share           float64 `json:"share_of_network"` // your hashrate / network hashrate
	PerBlock        float64 `json:"per_block"`
	PerDay          float64 `json:"per_day"`
	PerWeek         float64 `json:"per_week"`
	PerYear         float64 `json:"per_year"`
	ExpectedPerYear float64 `json:"expected_blocks_per_year"`
	YearsPerBlock   float64 `json:"years_per_block"`
	OneInPerDay     float64 `json:"one_in_per_day"`
	OneInPerBlock   float64 `json:"one_in_per_block"`
	BestShare       float64 `json:"best_share,omitempty"`
	BestShareClose  float64 `json:"best_share_closeness,omitempty"`
}

// Compute fills Odds for hashrate h (H/s), difficulty d and an optional best share.
func Compute(h, d, best float64) Odds {
	o := Odds{HashrateHs: h, Difficulty: d}
	if d <= 0 {
		return o
	}
	o.HashesPerBlock = HashesPerBlock(d)
	o.NetworkHs = NetworkHashrate(d)
	if h > 0 {
		o.Share = h / o.NetworkHs
		o.PerBlock = Chance(h, d, BlockInterval)
		o.PerDay = Chance(h, d, Day)
		o.PerWeek = Chance(h, d, 7*Day)
		o.PerYear = Chance(h, d, Year)
		o.ExpectedPerYear = Lambda(h, d, Year)
		o.YearsPerBlock = SecondsPerBlock(h, d) / Year
		o.OneInPerDay = OneIn(o.PerDay)
		o.OneInPerBlock = OneIn(o.PerBlock)
	}
	if best > 0 {
		o.BestShare = best
		o.BestShareClose = Closeness(best, d)
	}
	return o
}

// Reward is the value of winning a block now.
type Reward struct {
	Height     int64   `json:"height"`
	SubsidySat int64   `json:"subsidy_sat"`
	FeesSat    int64   `json:"fees_sat"` // average total fees of recent blocks
	TotalSat   int64   `json:"total_sat"`
	USD        float64 `json:"usd,omitempty"` // 0 when no price
	PriceUSD   float64 `json:"price_usd,omitempty"`
}

// BlockReward is subsidy(next height) + average recent fees, valued at price (USD/BTC, optional).
func BlockReward(tipHeight int64, avgFeesSat int64, priceUSD float64) Reward {
	r := Reward{Height: tipHeight + 1, SubsidySat: Subsidy(tipHeight + 1), FeesSat: max(avgFeesSat, 0)}
	r.TotalSat = r.SubsidySat + r.FeesSat
	if priceUSD > 0 {
		r.PriceUSD = priceUSD
		r.USD = float64(r.TotalSat) / 1e8 * priceUSD
	}
	return r
}

// CoinFlips is how many heads in a row have the same odds as a 1-in-N chance
// (round(log2(N))). Zero when N is not greater than 1.
func CoinFlips(oneIn float64) int {
	if oneIn <= 1 {
		return 0
	}
	return int(math.Round(math.Log2(oneIn)))
}
