package mining

import (
	"fmt"
	"math"
	"strconv"
)

// FeedMax is how many share-feed rows the UI shows.
const FeedMax = 8

// FormatDiff is the compact difficulty used in the share feed (6.4K, 76.2M).
// It matches the front-end big() helper: one decimal when the scaled value is
// below 10, otherwise a whole number; values under 1000 are unscaled.
func FormatDiff(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	u := []string{"", "K", "M", "G", "T", "P", "E"}
	i := 0
	x := v
	for math.Abs(x) >= 1000 && i < len(u)-1 {
		x /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(int64(math.Round(x)), 10)
	}
	// One decimal, like the front-end si(v, 1), so 76,205,629 is "76.2M"
	// not "76M".
	return fmt.Sprintf("%.1f%s", x, u[i])
}

// RelAgo is a short relative time like the front-end rel() helper.
func RelAgo(t, now int64) string {
	if t <= 0 {
		return ""
	}
	s := now - t
	if s < 0 {
		s = 0
	}
	switch {
	case s < 5:
		return "just now"
	case s < 60:
		return fmt.Sprintf("%ds ago", s)
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh ago", s/3600)
	default:
		return fmt.Sprintf("%dd ago", s/86400)
	}
}

// FeedLine is one share-feed row. Difficulties are only shown when known
// (never invented). n>1 becomes "N shares found · latest X".
func FeedLine(h Hit, now int64) string {
	n := h.N
	if n < 1 {
		n = 1
	}
	var head string
	if n == 1 {
		head = "Share found"
	} else {
		head = fmt.Sprintf("%d shares found", n)
	}
	diff := FormatDiff(h.Diff)
	if diff != "" {
		if n == 1 {
			head += " · " + diff
		} else {
			head += " · latest " + diff
		}
	}
	if ago := RelAgo(h.T, now); ago != "" {
		head += " · " + ago
	}
	return head
}

// IsNewBest is true when this hit's known difficulty is the best share (the
// miner-reported best, not an invented value).
func IsNewBest(h Hit, best float64) bool {
	if h.Diff <= 0 || best <= 0 {
		return false
	}
	// Same share: allow a tiny relative difference from float rounding.
	return h.Diff >= best*0.999 && h.Diff <= best*1.001
}

// NewestFirst returns the most recent hits first, capped at FeedMax, without
// inventing rows or difficulties.
func NewestFirst(hits []Hit, max int) []Hit {
	if max <= 0 {
		max = FeedMax
	}
	n := len(hits)
	if n == 0 {
		return nil
	}
	if n > max {
		hits = hits[n-max:]
		n = max
	}
	out := make([]Hit, n)
	for i := 0; i < n; i++ {
		out[i] = hits[n-1-i]
	}
	return out
}
