package mining

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Words is a short English amount ("5.1 trillion") matching the front-end words() helper.
func Words(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	names := []string{"", " thousand", " million", " billion", " trillion", " quadrillion", " quintillion", " sextillion", " septillion"}
	i := 0
	for v >= 1000 && i < len(names)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(int64(math.Round(v)), 10)
	}
	if v < 10 {
		return fmt.Sprintf("%.1f%s", v, names[i])
	}
	return fmt.Sprintf("%d%s", int(math.Round(v)), names[i])
}

// Comma groups a block height with thousands separators (977,100).
func Comma(n int64) string {
	if n < 0 {
		return "-" + Comma(-n)
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre == 0 {
		pre = 3
	}
	b.WriteString(s[:pre])
	for i := pre; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// StoryLine is the self-explaining sentence at the top of the Mining view.
// hashrate is in TH/s (chip-reported). When height is unknown the block number
// is left off rather than invented.
func StoryLine(ths float64, height int64, hasHeight bool) string {
	var blk string
	if hasHeight && height > 0 {
		blk = " for Block " + Comma(height)
	}
	if ths > 0 {
		return "Your miner is guessing about " + strings.TrimSpace(Words(ths*1e12)) + " numbers a second, trying to find a winning one" + blk + "."
	}
	if blk != "" {
		return "Your miner is trying to find a winning one" + blk + "."
	}
	return "Your miner is trying to find a winning Bitcoin block."
}
