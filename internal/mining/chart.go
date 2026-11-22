package mining

// MinWindow is the shortest time axis the share-hits chart will use, so a
// few minutes of data are not stretched across an empty hour.
const MinWindow = 45 * 60

// MaxWindow matches the 24 h history the log keeps.
const MaxWindow = Keep

// TimeWindow returns the [t0, t1] axis for the share-hits chart: it fits the
// actual hits, but is never shorter than minWindow or longer than maxWindow.
// t1 is "now" (or the last hit if that is later).
func TimeWindow(hits []Hit, now, minWindow, maxWindow int64) (t0, t1 int64) {
	if minWindow <= 0 {
		minWindow = MinWindow
	}
	if maxWindow <= 0 {
		maxWindow = MaxWindow
	}
	t1 = now
	var first, last int64
	n := 0
	for _, h := range hits {
		if h.T <= 0 || h.Diff <= 0 {
			continue
		}
		if n == 0 || h.T < first {
			first = h.T
		}
		if n == 0 || h.T > last {
			last = h.T
		}
		n++
	}
	if n == 0 {
		return t1 - minWindow, t1
	}
	if last > t1 {
		t1 = last
	}
	t0 = first - 30
	if t1-t0 < minWindow {
		t0 = t1 - minWindow
	}
	if t1-t0 > maxWindow {
		t0 = t1 - maxWindow
	}
	return t0, t1
}

// BucketSeconds is the aggregation bucket for a window of the given length.
func BucketSeconds(window int64) int64 {
	switch {
	case window >= 12*3600:
		return 300
	case window >= 3*3600:
		return 120
	default:
		return 60
	}
}

// BucketHits merges shares that landed in the same time bucket when their
// difficulties are within a factor of two (typical pool-diff shares). A
// distinctly harder share stays its own point. Each point uses the latest
// known difficulty and the summed count; other difficulties are not invented.
func BucketHits(hits []Hit, bucket int64) []Hit {
	if len(hits) == 0 {
		return nil
	}
	if bucket < 1 {
		bucket = 30
	}
	type grp struct {
		k    int64
		hits []Hit
	}
	var order []int64
	by := map[int64][]Hit{}
	for _, h := range hits {
		if h.Diff <= 0 || h.T <= 0 {
			continue
		}
		k := h.T / bucket
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		if h.N < 1 {
			h.N = 1
		}
		by[k] = append(by[k], h)
	}
	var out []Hit
	for _, k := range order {
		out = append(out, clusterDiffs(by[k])...)
	}
	return out
}

func clusterDiffs(in []Hit) []Hit {
	if len(in) == 0 {
		return nil
	}
	// Insertion sort by difficulty so we can grow clusters in order.
	s := append([]Hit(nil), in...)
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j].Diff < s[j-1].Diff {
			s[j], s[j-1] = s[j-1], s[j]
			j--
		}
	}
	var groups [][]Hit
	for _, h := range s {
		if n := len(groups); n == 0 {
			groups = append(groups, []Hit{h})
			continue
		}
		g := groups[len(groups)-1]
		lo, hi := g[0].Diff, g[0].Diff
		for _, x := range g {
			if x.Diff < lo {
				lo = x.Diff
			}
			if x.Diff > hi {
				hi = x.Diff
			}
		}
		if h.Diff > hi*2 || lo > h.Diff*2 {
			groups = append(groups, []Hit{h})
			continue
		}
		groups[len(groups)-1] = append(g, h)
	}
	out := make([]Hit, 0, len(groups))
	for _, g := range groups {
		latest := g[0]
		n := 0
		for _, h := range g {
			n += h.N
			if h.T >= latest.T {
				latest = h
			}
		}
		if n < 1 {
			n = 1
		}
		out = append(out, Hit{T: latest.T, Diff: latest.Diff, N: n})
	}
	return out
}

// ShareAxis is the log-Y range for share points, pool difficulty and best
// share. Network difficulty is left off this scale when it would crush the
// shares; the UI then draws it as a labeled marker at the top.
func ShareAxis(pool, best, net float64, hits []Hit) (ymin, ymax float64, netSeparate bool) {
	vs := make([]float64, 0, 4+len(hits))
	if pool > 0 {
		vs = append(vs, pool)
	}
	if best > 0 {
		vs = append(vs, best)
	}
	for _, h := range hits {
		if h.Diff > 0 {
			vs = append(vs, h.Diff)
		}
	}
	if len(vs) == 0 {
		if net > 0 {
			return 1, net, false
		}
		return 1, 10, false
	}
	lo, hi := vs[0], vs[0]
	for _, v := range vs {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	ymin = lo / 2
	if ymin < 1 {
		ymin = 1
	}
	ymax = hi * 3
	if ymax <= ymin {
		ymax = ymin * 10
	}
	netSeparate = net > 0 && net > ymax*1.5
	if net > 0 && !netSeparate {
		ymax = net * 1.05
	}
	return ymin, ymax, netSeparate
}

// ReportedBest is the best-share value the Mining view should show: the
// miner's current Best Share, HashWatch's stored best (so a reboot doesn't
// forget one), and the lottery/leaderboard best. Last-share hit points are
// not a source — they can disagree with the miner's Best Share.
func ReportedBest(miner, stored, lottery float64) float64 {
	b := miner
	if stored > b {
		b = stored
	}
	if lottery > b {
		b = lottery
	}
	if b < 0 {
		return 0
	}
	return b
}
