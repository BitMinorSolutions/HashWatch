package mining

import (
	"math"
	"sort"
)

// LogPos is a value's position on a log scale from 1 to max (0..1).
// Values below 1 sit at the left edge; values at or above max sit at the right.
func LogPos(v, max float64) float64 {
	if v <= 0 || max <= 0 {
		return 0
	}
	if v >= max {
		return 1
	}
	const min = 1.0
	if max <= min {
		return 1
	}
	if v < min {
		return 0
	}
	return math.Log(v/min) / math.Log(max/min)
}

// Marker is a named point on the closeness meter.
type Marker struct {
	ID    string // pool, today, ever, win
	Label string // full label
	Short string // narrow-screen label; empty means use Label
	Value float64
}

// Placed is a marker after layout: X is 0..1, Side is "above" or "below",
// Align is "center", "start" (left-aligned at X) or "end" (right-aligned at X).
type Placed struct {
	ID, Label, Side, Align string
	X, Value               float64
}

// CharW and LabelPad are the estimated px width of one character and extra
// padding used to keep labels from overlapping. The JS meter uses the same
// numbers.
const (
	CharW      = 5.4
	LabelPad   = 8.0
	NarrowPx   = 520.0
	MinGapPx   = 4.0
	DefaultMax = 1.0
)

// LabelWidth estimates how wide a label will paint.
func LabelWidth(s string) float64 {
	if s == "" {
		return 0
	}
	return float64(len(s))*CharW + LabelPad
}

// Place lays out meter labels so they do not overlap, including at phone
// width. widthPx is the bar width. max is the right-end value (network
// difficulty); markers with Value <= 0 are omitted. The "win" marker, if
// present, is pinned to the right end and right-aligned.
func Place(markers []Marker, max, widthPx float64) []Placed {
	if widthPx < 1 {
		widthPx = 1
	}
	useShort := widthPx < NarrowPx
	var out []Placed
	for _, m := range markers {
		if m.Value <= 0 && m.ID != "win" {
			continue
		}
		if m.ID != "win" && max <= 0 {
			// Without a known max we can still place relative markers if the
			// caller passed a display max; max <= 0 means skip.
			continue
		}
		lab := m.Label
		if useShort && m.Short != "" {
			lab = m.Short
		}
		x := 1.0
		if m.ID != "win" {
			if max <= 0 {
				continue
			}
			x = LogPos(m.Value, max)
		} else if max <= 0 {
			continue
		}
		out = append(out, Placed{ID: m.ID, Label: lab, X: x, Value: m.Value, Side: "above", Align: "center"})
	}
	if len(out) == 0 {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		// Keep "win" last among ties at the right.
		if out[i].ID == "win" {
			return false
		}
		if out[j].ID == "win" {
			return true
		}
		return out[i].ID < out[j].ID
	})
	// Pin the winning-block label to the right, right-aligned, above.
	for i := range out {
		if out[i].ID == "win" {
			out[i].X = 1
			out[i].Align = "end"
			out[i].Side = "above"
		}
	}
	// Alternate other markers: leftmost below (away from the top-right cap),
	// then above, then below. Markers at the same X go on opposite sides.
	n := 0
	for i := range out {
		if out[i].ID == "win" {
			continue
		}
		if n%2 == 0 {
			out[i].Side = "below"
		} else {
			out[i].Side = "above"
		}
		n++
		if i > 0 && nearX(out[i].X, out[i-1].X) && out[i-1].ID != "win" {
			if out[i-1].Side == "below" {
				out[i].Side = "above"
			} else {
				out[i].Side = "below"
			}
		}
	}
	clampAlign(out, widthPx)
	resolveOverlaps(out, widthPx, markers, useShort)
	clampAlign(out, widthPx)
	resolveOverlaps(out, widthPx, markers, useShort)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].ID == "win" || out[j].ID == "win" {
				continue
			}
			if nearX(out[i].X, out[j].X) && out[i].Side == out[j].Side {
				out[j].Side = other(out[i].Side)
			}
		}
	}
	return out
}

func nearX(a, b float64) bool { return math.Abs(a-b) < 0.02 }

func clampAlign(out []Placed, width float64) {
	for i := range out {
		p := &out[i]
		if p.ID == "win" {
			p.Align = "end"
			continue
		}
		w := LabelWidth(p.Label)
		x := p.X * width
		left, right := span(*p, width)
		if left < 0 && right > width {
			// impossibly wide; keep center and let CSS overflow wrap? stay start.
			p.Align = "start"
			continue
		}
		if left < 0 {
			p.Align = "start"
			// if start still overruns the right, nothing else to do
			_ = x
		} else if right > width {
			p.Align = "end"
		} else if p.Align != "end" && p.Align != "start" {
			p.Align = "center"
		}
		// After a previous end/start, a clamp toward center if it now fits.
		if p.Align == "start" && x-w/2 >= 0 && x+w/2 <= width {
			p.Align = "center"
		}
		if p.Align == "end" && p.ID != "win" && x-w/2 >= 0 && x+w/2 <= width {
			p.Align = "center"
		}
	}
}

func span(p Placed, width float64) (left, right float64) {
	w := LabelWidth(p.Label)
	x := p.X * width
	switch p.Align {
	case "start":
		return x, x + w
	case "end":
		return x - w, x
	default:
		return x - w/2, x + w/2
	}
}

func overlaps(a, b Placed, width float64) bool {
	if a.Side != b.Side {
		return false
	}
	a0, a1 := span(a, width)
	b0, b1 := span(b, width)
	return a0 < b1-MinGapPx && b0 < a1-MinGapPx
}

func resolveOverlaps(out []Placed, width float64, src []Marker, usedShort bool) {
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if !overlaps(out[i], out[j], width) {
				continue
			}
			// Prefer flipping the non-win marker.
			flip := j
			if out[j].ID == "win" {
				flip = i
			}
			out[flip].Side = other(out[flip].Side)
			if !overlaps(out[i], out[j], width) {
				continue
			}
			// Flip didn't help (or created a same-side pair with a third later);
			// try short labels if we weren't already short.
			if !usedShort {
				if s := shortOf(src, out[i].ID); s != "" {
					out[i].Label = s
				}
				if s := shortOf(src, out[j].ID); s != "" {
					out[j].Label = s
				}
				clampAlign(out, width)
			}
			if !overlaps(out[i], out[j], width) {
				continue
			}
			// Last resort: keep them on opposite sides (already flipped) and
			// nudge the later label's alignment toward the edge if needed.
			if out[i].Side == out[j].Side {
				out[j].Side = other(out[i].Side)
			}
		}
	}
}

func other(side string) string {
	if side == "above" {
		return "below"
	}
	return "above"
}

func shortOf(src []Marker, id string) string {
	for _, m := range src {
		if m.ID == id && m.Short != "" {
			return m.Short
		}
	}
	return ""
}

// DisplayMax is the right-end value of the meter. When network difficulty is
// known it is used; otherwise 0 (the caller should hide the block end).
func DisplayMax(net float64) float64 {
	if net > 0 {
		return net
	}
	return 0
}

// CloseText is the plain sentence under the meter. empty if net or best is unknown.
func CloseText(best, net float64, pct string) string {
	if best <= 0 || net <= 0 || pct == "" {
		return ""
	}
	return "Your best share is " + pct + " of the way to winning a block."
}
