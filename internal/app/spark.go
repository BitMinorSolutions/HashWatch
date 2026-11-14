package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"time"
)

// SparklinePNG draws the last hour of hashrate (all online miners summed, 5-minute
// average) as a small black-on-transparent PNG for the Mac menu bar (a "template"
// image, so macOS tints it for light and dark menu bars). w×h are pixels (use 2× for
// Retina). It returns nil when the sparkline is off or there's too little history.
func (s *Server) SparklinePNG(w, h int) []byte {
	if s.Cfg.Fun.MenuSparklineOff {
		return nil
	}
	cut := time.Now().Unix() - 3600
	const buckets = 30
	var sum [buckets]float64
	var cnt [buckets]int
	for _, m := range s.Cfg.Miners {
		v, ok := s.Poll.Live(m.ID, cut)
		if !ok {
			continue
		}
		for _, x := range v.History {
			if x.H5m == nil {
				continue
			}
			b := int(float64(x.T-cut) / 3600 * buckets)
			if b >= 0 && b < buckets {
				sum[b] += *x.H5m
				cnt[b]++
			}
		}
	}
	var pts []float64
	for i := range sum {
		if cnt[i] > 0 {
			pts = append(pts, sum[i]/float64(cnt[i]))
		} else {
			pts = append(pts, math.NaN())
		}
	}
	return Sparkline(pts, w, h)
}

// Sparkline renders values (NaN = gap) as an anti-aliased-ish 2 px polyline.
func Sparkline(vals []float64, w, h int) []byte {
	lo, hi, n := math.Inf(1), math.Inf(-1), 0
	for _, v := range vals {
		if !math.IsNaN(v) {
			lo, hi, n = math.Min(lo, v), math.Max(hi, v), n+1
		}
	}
	if n < 3 || w < 8 || h < 6 {
		return nil
	}
	mid := (lo + hi) / 2
	if span := math.Max(math.Abs(mid)*0.08, 1e-9); hi-lo < span { // don't blow noise up into a mountain range
		lo, hi = mid-span/2, mid+span/2
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	pad := 2.0
	x := func(i int) float64 { return pad + float64(i)/float64(len(vals)-1)*(float64(w)-2*pad) }
	y := func(v float64) float64 { return pad + (1-(v-lo)/(hi-lo))*(float64(h)-2*pad) }
	plot := func(px, py float64) {
		for dx := -1.0; dx <= 1.0; dx += 1 {
			for dy := -1.0; dy <= 1.0; dy += 1 {
				d := math.Hypot(dx, dy)
				a := uint8(255 * math.Max(0, 1-d*0.55))
				ix, iy := int(math.Round(px+dx)), int(math.Round(py+dy))
				if image.Pt(ix, iy).In(img.Rect) && img.NRGBAAt(ix, iy).A < a {
					img.SetNRGBA(ix, iy, color.NRGBA{0, 0, 0, a})
				}
			}
		}
	}
	prev := -1
	for i, v := range vals {
		if math.IsNaN(v) {
			prev = -1
			continue
		}
		if prev >= 0 {
			x0, y0, x1, y1 := x(prev), y(vals[prev]), x(i), y(v)
			steps := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))*2) + 1
			for k := 0; k <= steps; k++ {
				t := float64(k) / float64(steps)
				plot(x0+(x1-x0)*t, y0+(y1-y0)*t)
			}
		} else {
			plot(x(i), y(v))
		}
		prev = i
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
