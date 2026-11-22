package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/hashwatch/hashwatch/internal/lottery"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
)

// DefaultDailySummaryAt is the local time of the last-24-hours briefing.
const DefaultDailySummaryAt = "09:00"

// dailyGap is the longest pause between history samples that still counts as
// online. It covers Gentle mode (30 s) plus a missed poll or two.
const dailyGap = 3 * time.Minute

func parseDailyAt(s string) (h, m int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 9, 0, true
	}
	s = strings.ReplaceAll(s, ".", ":")
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	mm, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

func normalizeDailyAt(s string) (string, error) {
	h, m, ok := parseDailyAt(s)
	if !ok {
		return "", fmt.Errorf("Use a local time like 09:00.")
	}
	return fmt.Sprintf("%02d:%02d", h, m), nil
}

func dailyDue(now time.Time, at, last string) bool {
	if last == now.Format("2006-01-02") {
		return false
	}
	h, m, ok := parseDailyAt(at)
	if !ok {
		h, m = 9, 0
	}
	target := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	return !now.Before(target)
}

func sampleTHs(s model.Sample) float64 {
	for _, p := range []*float64{s.H5m, s.H15m, s.H1m, s.H5s} {
		if p != nil && *p > 0 {
			return *p
		}
	}
	return 0
}

type dailyWindow struct {
	UptimePct float64
	AvgTHs    float64
	TopTemp   float64
	HaveTemp  bool
	BestShare float64
	SpanSec   float64
	OnlineSec float64
}

func summarize24h(hist []model.Sample, now time.Time, best float64) dailyWindow {
	cut := now.Add(-24 * time.Hour).Unix()
	w := dailyWindow{BestShare: best}
	var first int64
	var prev int64
	for _, s := range hist {
		if s.T < cut {
			continue
		}
		if first == 0 || s.T < first {
			first = s.T
		}
		if prev > 0 {
			dt := s.T - prev
			if dt > 0 && dt <= int64(dailyGap.Seconds()) {
				w.OnlineSec += float64(dt)
				if h := sampleTHs(s); h > 0 {
					w.AvgTHs += h * float64(dt)
				}
			}
		}
		prev = s.T
		if s.TMax != nil && *s.TMax > w.TopTemp {
			w.TopTemp, w.HaveTemp = *s.TMax, true
		}
	}
	if w.OnlineSec > 0 {
		w.AvgTHs /= w.OnlineSec
	}
	start := cut
	if first > start {
		start = first
	}
	w.SpanSec = math.Max(0, float64(now.Unix()-start))
	if w.SpanSec > 0 {
		w.UptimePct = math.Min(100, w.OnlineSec/w.SpanSec*100)
	}
	return w
}

func fmtHorizon(years float64) string {
	if years <= 0 || math.IsNaN(years) || math.IsInf(years, 0) {
		return ""
	}
	switch {
	case years < 1.0/365:
		h := years * 365 * 24
		if h < 1 {
			return fmt.Sprintf("%.0f minutes", years*365*24*60)
		}
		return fmt.Sprintf("%.0f hours", h)
	case years < 1.0/12:
		return fmt.Sprintf("%.0f days", years*365)
	case years < 2:
		return fmt.Sprintf("%.0f months", years*12)
	default:
		return siCount(years) + " years"
	}
}

func dailyBody(name string, w dailyWindow, odds lottery.Odds) (title, body string) {
	title = "Daily summary"
	if name != "" {
		title += " · " + name
	}
	var parts []string
	if w.SpanSec > 0 {
		parts = append(parts, fmt.Sprintf("online %.0f%%", w.UptimePct))
	}
	if w.AvgTHs > 0 {
		parts = append(parts, fmt.Sprintf("avg %.2f TH/s", w.AvgTHs))
	}
	if w.HaveTemp {
		parts = append(parts, fmt.Sprintf("top %.0f°C", w.TopTemp))
	}
	if w.BestShare > 0 {
		parts = append(parts, "best share "+siDiff(w.BestShare))
	}
	if odds.OneInPerDay > 0 && odds.YearsPerBlock > 0 {
		parts = append(parts, fmt.Sprintf("1 in %s today, about %s to a block", siCount(odds.OneInPerDay), fmtHorizon(odds.YearsPerBlock)))
	} else if odds.Difficulty <= 0 || odds.HashrateHs <= 0 {
		parts = append(parts, "odds unavailable (need hashrate and network difficulty)")
	}
	if len(parts) == 0 {
		body = "Not enough history yet for a 24-hour summary."
	} else {
		body = strings.ToUpper(parts[0][:1]) + parts[0][1:]
		if len(parts) > 1 {
			body += " · " + strings.Join(parts[1:], " · ")
		}
		body += "."
	}
	return title, body
}

// MaybeDailySummary fires the last-24-hours briefing once a day at the
// configured local time (default 09:00). Midnight calendar-day rollover is unchanged.
func (s *Stats) MaybeDailySummary(id, name string, snap *model.Snapshot, hist []model.Sample, at string) {
	if s == nil {
		return
	}
	now := s.Now()
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	ms := s.get(id)
	if !dailyDue(now, at, ms.LastDailySummary) {
		s.mu.Unlock()
		return
	}
	// Need a little history, or a clearly empty 24 h, before locking in the day.
	if len(hist) == 0 && (snap == nil || !snap.OK) {
		s.mu.Unlock()
		return
	}
	best := ms.bestSince(now.Add(-24 * time.Hour).Unix())
	if snap != nil {
		if v, ok := fnum(snap.Shares.BestShare); ok && v > best {
			// session best has no timestamp; only use it if we have no 24h best
			if best == 0 {
				best = v
			}
		}
	}
	w := summarize24h(hist, now, best)
	if w.SpanSec < 15*60 && w.OnlineSec < 15*60 {
		s.mu.Unlock()
		return // try again later today once there is something to say
	}
	var net netdata.Snapshot
	if s.Net != nil {
		net = s.Net()
	}
	var hs float64
	if snap != nil && snap.OK {
		hs = oddsHashrate(snap)
	}
	if hs == 0 && w.AvgTHs > 0 {
		hs = w.AvgTHs * 1e12
	}
	odds := lottery.Compute(hs, net.Difficulty, w.BestShare)
	title, body := dailyBody(name, w, odds)
	ms.LastDailySummary = now.Format("2006-01-02")
	s.mu.Unlock()
	if s.Alerts != nil {
		s.Alerts.Fire(Alert{Miner: id, MinerName: name, Kind: "summary", Level: "info", Title: title, Body: body})
	}
}

func (ms *MinerStats) bestSince(cut int64) float64 {
	var best float64
	for _, r := range ms.Leaderboard {
		if r.T >= cut && r.Diff > best {
			best = r.Diff
		}
	}
	return best
}
