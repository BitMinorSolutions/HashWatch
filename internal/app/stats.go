package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/lottery"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/netdata"
)

// Stats keeps each miner's lottery record on this computer: tickets (hashes
// tried), the best-share leaderboard, found blocks, daily totals, streaks and
// badges, and daily/weekly summaries. Nothing here is sent anywhere.
type Stats struct {
	Now    func() time.Time
	Alerts *Alerts
	Net    func() netdata.Snapshot // current network data (zero = unknown)
	Fun    func() FunSettings
	// OnEvent tells the automations about "best" (new personal best) and "block" events.
	OnEvent func(id, kind string)

	dir string
	mu  sync.Mutex
	m   map[string]*MinerStats
}

// ShareRec is one leaderboard entry: the miner's best share of a session.
type ShareRec struct {
	Diff    float64 `json:"diff"`
	T       int64   `json:"t"`
	NetDiff float64 `json:"net_diff,omitempty"` // network difficulty when it was found (0 = unknown)
}

// DayRec is one local calendar day.
type DayRec struct {
	Date      string  `json:"date"` // 2006-01-02, local time
	Hashes    float64 `json:"hashes"`
	OnlineSec float64 `json:"online_sec"`
	SeenSec   float64 `json:"seen_sec"` // time HashWatch was watching (online or not)
	Expected  float64 `json:"expected"` // sum of λ (expected blocks) for the day
	BestShare float64 `json:"best_share"`
	Blocks    int     `json:"blocks"`
}

type Summary struct {
	Kind      string  `json:"kind"` // daily | weekly
	From      string  `json:"from"`
	To        string  `json:"to"`
	AvgTHs    float64 `json:"avg_ths"`
	UptimePct float64 `json:"uptime_pct"`
	BestShare float64 `json:"best_share"`
	Hashes    float64 `json:"hashes"`
	Expected  float64 `json:"expected"`
	Blocks    int     `json:"blocks"`
	T         int64   `json:"t"`
}

type BlockRec struct {
	T     int64   `json:"t"`
	Count float64 `json:"count"` // Found Blocks counter after the find
}

type MinerStats struct {
	FirstSeen        int64            `json:"first_seen"`
	LastPoll         int64            `json:"last_poll"`
	LastUptime       int64            `json:"last_uptime"`
	SessionBest      float64          `json:"session_best"`
	Leaderboard      []ShareRec       `json:"leaderboard"`
	TicketsAll       float64          `json:"tickets_all"`
	ExpectedAll      float64          `json:"expected_all"`
	Days             []DayRec         `json:"days"`
	Blocks           []BlockRec       `json:"blocks"`
	FoundCounter     float64          `json:"found_counter"`
	HaveFound        bool             `json:"have_found"`
	Badges           map[string]int64 `json:"badges"`
	Summaries        []Summary        `json:"summaries"`
	LastWeekly       string           `json:"last_weekly"`
	LastDailySummary string           `json:"last_daily_summary,omitempty"` // local date 2006-01-02 last 9am briefing was sent
	PBCount          int              `json:"pb_count"`                     // times the personal best was beaten
}

const (
	leaderMax  = 10
	daysKept   = 120
	maxGap     = 90.0 // seconds; longer gaps between polls aren't counted as hashing time
	streakFrac = 20.0 / 24.0
)

func NewStats(dir string) *Stats {
	return &Stats{dir: dir, m: map[string]*MinerStats{}, Now: time.Now}
}

func (s *Stats) file(id string) string { return filepath.Join(s.dir, "stats-"+id+".json") }

func (s *Stats) get(id string) *MinerStats {
	ms := s.m[id]
	if ms == nil {
		ms = &MinerStats{Badges: map[string]int64{}}
		if b, err := os.ReadFile(s.file(id)); err == nil {
			_ = json.Unmarshal(b, ms)
		}
		if ms.Badges == nil {
			ms.Badges = map[string]int64{}
		}
		s.m[id] = ms
	}
	return ms
}

// Save writes every miner's stats.
func (s *Stats) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ms := range s.m {
		b, _ := json.Marshal(ms)
		tmp := s.file(id) + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, s.file(id))
		}
	}
}

// Remove forgets a miner.
func (s *Stats) Remove(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
	os.Remove(s.file(id))
}

func (ms *MinerStats) day(date string) *DayRec {
	if n := len(ms.Days); n > 0 && ms.Days[n-1].Date == date {
		return &ms.Days[n-1]
	}
	ms.Days = append(ms.Days, DayRec{Date: date})
	if len(ms.Days) > daysKept {
		ms.Days = ms.Days[len(ms.Days)-daysKept:]
	}
	return &ms.Days[len(ms.Days)-1]
}

// hashrateHs picks the steadiest hashrate the miner reports, in H/s.
func hashrateHs(snap *model.Snapshot) float64 {
	h := snap.Hashrate
	for _, p := range []*float64{h.THs5m, h.THs1m, h.THs15m, h.THs5s, h.THsHero} {
		if p != nil && *p > 0 {
			return *p * 1e12
		}
	}
	return 0
}

// oddsHashrate is the hashrate used for the odds, in H/s: the same live
// headline as the dashboard (GHSspd / ths_hero), then MHS 1m, then MHS 5m.
// A present-but-zero GHSspd is a live reading of "not hashing", not a reason
// to fall back to a share-based average.
func oddsHashrate(snap *model.Snapshot) float64 {
	if snap == nil {
		return 0
	}
	h := snap.Hashrate
	if h.THsHero != nil {
		if *h.THsHero > 0 {
			return *h.THsHero * 1e12
		}
		return 0
	}
	if h.GHsSpd != nil {
		if *h.GHsSpd > 0 {
			return *h.GHsSpd * 1e9
		}
		return 0
	}
	for _, p := range []*float64{h.THs1m, h.THs5m} {
		if p != nil && *p > 0 {
			return *p * 1e12
		}
	}
	return 0
}

type pending struct {
	alerts []Alert
	events []string
}

// Observe updates one miner's record after a poll.
func (s *Stats) Observe(id, name string, snap *model.Snapshot) {
	now := s.Now()
	var net netdata.Snapshot
	if s.Net != nil {
		net = s.Net()
	}
	var fun FunSettings
	if s.Fun != nil {
		fun = s.Fun()
	}
	s.mu.Lock()
	ms := s.get(id)
	p := s.observeLocked(ms, id, name, snap, now, net, fun)
	s.mu.Unlock()
	for _, a := range p.alerts {
		if s.Alerts != nil {
			s.Alerts.Fire(a)
		}
	}
	for _, e := range p.events {
		if s.OnEvent != nil {
			s.OnEvent(id, e)
		}
	}
}

func (s *Stats) observeLocked(ms *MinerStats, id, name string, snap *model.Snapshot, now time.Time, net netdata.Snapshot, fun FunSettings) pending {
	var p pending
	base := Alert{Miner: id, MinerName: name}
	if ms.FirstSeen == 0 {
		ms.FirstSeen = now.Unix()
	}
	date := now.Format("2006-01-02")
	if n := len(ms.Days); n > 0 && ms.Days[n-1].Date != date {
		p.alerts = append(p.alerts, s.rollover(ms, base, now, fun)...)
	}
	d := ms.day(date)
	dt := 0.0
	if ms.LastPoll > 0 {
		dt = float64(now.Unix() - ms.LastPoll)
		if dt < 0 || dt > maxGap {
			dt = 0
		}
	}
	ms.LastPoll = now.Unix()
	d.SeenSec += dt
	if snap == nil || !snap.OK {
		return p
	}
	// Tickets: hashes tried = hashrate × time.
	h := hashrateHs(snap)
	if dt > 0 && h > 0 {
		hashes := h * dt
		d.Hashes += hashes
		d.OnlineSec += dt
		ms.TicketsAll += hashes
		if net.Difficulty > 0 {
			lam := lottery.Lambda(h, net.Difficulty, dt)
			d.Expected += lam
			ms.ExpectedAll += lam
		}
	}
	// A miner restart resets its session best share.
	if up := snap.Uptime.Seconds; up > 0 {
		if ms.LastUptime > 0 && up+30 < ms.LastUptime {
			ms.SessionBest = 0
		}
		ms.LastUptime = up
	}
	if b, ok := fnum(snap.Shares.BestShare); ok && b > ms.SessionBest {
		prevPB := 0.0
		if len(ms.Leaderboard) > 0 {
			prevPB = ms.Leaderboard[0].Diff
		}
		first := ms.SessionBest == 0 // first reading after start-up / a miner restart: not a fresh find
		ms.SessionBest = b
		ms.addShare(ShareRec{Diff: b, T: now.Unix(), NetDiff: net.Difficulty})
		if b > d.BestShare {
			d.BestShare = b
		}
		pct := ""
		if net.Difficulty > 0 {
			pct = fmt.Sprintf(" That's %s of a block.", pctText(lottery.Closeness(b, net.Difficulty)))
		}
		switch {
		case b > prevPB && prevPB > 0:
			ms.PBCount++
			p.events = append(p.events, "best")
			al := base
			al.Kind, al.Level, al.Title = "nearmiss", "celebrate", "New personal best share"
			al.Body = fmt.Sprintf("%s beat its best share ever: %s (was %s).%s", name, siDiff(b), siDiff(prevPB), pct)
			p.alerts = append(p.alerts, al)
		case !first && fun.NearMissDiff > 0 && b >= fun.NearMissDiff:
			al := base
			al.Kind, al.Level, al.Title = "nearmiss", "good", "Near miss!"
			al.Body = fmt.Sprintf("%s found a share of difficulty %s, above your %s threshold.%s", name, siDiff(b), siDiff(fun.NearMissDiff), pct)
			p.alerts = append(p.alerts, al)
		}
	}
	// Found blocks.
	if v, ok := fnum(snap.Shares.FoundBlocks); ok {
		if ms.HaveFound && v > ms.FoundCounter {
			ms.Blocks = append(ms.Blocks, BlockRec{T: now.Unix(), Count: v})
			d.Blocks += int(v - ms.FoundCounter)
			p.events = append(p.events, "block")
		}
		ms.FoundCounter, ms.HaveFound = v, true
	}
	if Optional.Badges { // OPTIONAL:badges
		p.alerts = append(p.alerts, s.badges(ms, base, now, net)...)
	}
	return p
}

func (ms *MinerStats) addShare(r ShareRec) {
	ms.Leaderboard = append(ms.Leaderboard, r)
	sort.SliceStable(ms.Leaderboard, func(i, j int) bool { return ms.Leaderboard[i].Diff > ms.Leaderboard[j].Diff })
	// one entry per value (the same session best seen twice)
	out := ms.Leaderboard[:0]
	for i, x := range ms.Leaderboard {
		if i > 0 && x.Diff == out[len(out)-1].Diff {
			continue
		}
		out = append(out, x)
	}
	if len(out) > leaderMax {
		out = out[:leaderMax]
	}
	ms.Leaderboard = out
}

// rollover closes the previous day(s) and produces the daily / weekly summary.
func (s *Stats) rollover(ms *MinerStats, base Alert, now time.Time, fun FunSettings) []Alert {
	var out []Alert
	prev := ms.Days[len(ms.Days)-1]
	kind := fun.Summary
	if kind == "" {
		kind = "daily"
	}
	if prev.SeenSec < 300 { // barely watched: nothing worth summarising
		return nil
	}
	mk := func(k string, days []DayRec) Summary {
		sm := Summary{Kind: k, From: days[0].Date, To: days[len(days)-1].Date, T: now.Unix()}
		var on, seen float64
		for _, d := range days {
			sm.Hashes += d.Hashes
			sm.Expected += d.Expected
			sm.Blocks += d.Blocks
			on += d.OnlineSec
			seen += d.SeenSec
			sm.BestShare = math.Max(sm.BestShare, d.BestShare)
		}
		if on > 0 {
			sm.AvgTHs = sm.Hashes / on / 1e12
		}
		if seen > 0 {
			sm.UptimePct = math.Min(100, on/seen*100)
		}
		return sm
	}
	add := func(sm Summary) {
		ms.Summaries = append(ms.Summaries, sm)
		if len(ms.Summaries) > 30 {
			ms.Summaries = ms.Summaries[len(ms.Summaries)-30:]
		}
		al := base
		al.Kind, al.Level = "summary", "info"
		when := "Yesterday"
		if sm.Kind == "weekly" {
			when = "This week"
		}
		al.Title = when + " on " + base.MinerName
		al.Body = fmt.Sprintf("Avg %.2f TH/s · online %.0f%% · best share %s · %s tickets.", sm.AvgTHs, sm.UptimePct, siDiff(sm.BestShare), siCount(sm.Hashes))
		if sm.Blocks > 0 {
			al.Body += fmt.Sprintf(" %d block%s found!", sm.Blocks, plural(float64(sm.Blocks), "", "s"))
		}
		out = append(out, al)
	}
	switch kind {
	case "daily":
		add(mk("daily", []DayRec{prev}))
	case "weekly":
		if int(now.Weekday()) == fun.SummaryWeekday && ms.LastWeekly != now.Format("2006-01-02") {
			n := min(7, len(ms.Days))
			add(mk("weekly", ms.Days[len(ms.Days)-n:]))
			ms.LastWeekly = now.Format("2006-01-02")
		}
	}
	return out
}

// Badge is one achievement.
type Badge struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Desc     string  `json:"desc"`
	Icon     string  `json:"icon"`
	Earned   int64   `json:"earned,omitempty"`
	Progress float64 `json:"progress"` // 0..1
	Hint     string  `json:"hint,omitempty"`
}

type badgeDef struct {
	id, title, desc, icon string
	prog                  func(ms *MinerStats, streak int, net netdata.Snapshot) (float64, string)
}

func best(ms *MinerStats) float64 {
	if len(ms.Leaderboard) == 0 {
		return 0
	}
	return ms.Leaderboard[0].Diff
}

func shareBadge(id, title string, diff float64) badgeDef {
	return badgeDef{id, title, "Find a share of difficulty " + siDiff(diff) + " or more.", "target", func(ms *MinerStats, _ int, _ netdata.Snapshot) (float64, string) {
		b := best(ms)
		if b <= 0 {
			return 0, ""
		}
		return math.Min(1, math.Log10(b)/math.Log10(diff)), "best " + siDiff(b)
	}}
}

func ticketBadge(id, title string, n float64) badgeDef {
	return badgeDef{id, title, "Try " + siCount(n) + " hashes (lottery tickets) in total.", "ticket", func(ms *MinerStats, _ int, _ netdata.Snapshot) (float64, string) {
		return math.Min(1, ms.TicketsAll/n), siCount(ms.TicketsAll) + " so far"
	}}
}

func streakBadge(id, title string, days int) badgeDef {
	return badgeDef{id, title, fmt.Sprintf("Stay online %d days in a row (20 h+ each day).", days), "flame", func(_ *MinerStats, streak int, _ netdata.Snapshot) (float64, string) {
		return math.Min(1, float64(streak)/float64(days)), fmt.Sprintf("%d/%d days", min(streak, days), days)
	}}
}

var badgeDefs = []badgeDef{
	{"first_block", "Block finder", "Find a Bitcoin block. The jackpot.", "trophy", func(ms *MinerStats, _ int, _ netdata.Snapshot) (float64, string) {
		if len(ms.Blocks) > 0 {
			return 1, ""
		}
		return 0, "keep the tickets coming"
	}},
	{"first_day", "Day one", "Watch your miner for a full day.", "sunrise", func(ms *MinerStats, _ int, _ netdata.Snapshot) (float64, string) {
		var on float64
		for _, d := range ms.Days {
			on += d.OnlineSec
		}
		return math.Min(1, on/86400), fmt.Sprintf("%.1f h online", on/3600)
	}},
	streakBadge("streak_7", "One week streak", 7),
	streakBadge("streak_30", "Month streak", 30),
	{"new_pb", "Personal best", "Beat your own best share.", "trending-up", func(ms *MinerStats, _ int, _ netdata.Snapshot) (float64, string) {
		if ms.PBCount > 0 {
			return 1, ""
		}
		return 0, "needs a second, better share"
	}},
	shareBadge("share_1g", "Giga share", 1e9),
	shareBadge("share_1t", "Tera share", 1e12),
	ticketBadge("tickets_1e18", "Exa ticket club", 1e18),
	ticketBadge("tickets_1e20", "Hundred exa", 1e20),
	{"close_1e5", "Within 0.001%", "Find a share at least 0.001% of the network difficulty.", "crosshair", func(ms *MinerStats, _ int, net netdata.Snapshot) (float64, string) {
		if net.Difficulty <= 0 || best(ms) <= 0 {
			return 0, "needs network difficulty"
		}
		c := lottery.Closeness(best(ms), net.Difficulty)
		return math.Min(1, c/1e-5), pctText(c) + " so far"
	}},
}

// Streak is consecutive days with ≥20 h online, plus today when today is on track
// (online at least 80% of the time HashWatch has been watching).
func (ms *MinerStats) Streak(now time.Time) int {
	idx := map[string]DayRec{}
	for _, d := range ms.Days {
		idx[d.Date] = d
	}
	n := 0
	if d, ok := idx[now.Format("2006-01-02")]; ok && d.OnlineSec > 0 && d.OnlineSec >= 0.8*d.SeenSec {
		n = 1
	}
	for k := 1; k <= daysKept; k++ {
		d, ok := idx[now.AddDate(0, 0, -k).Format("2006-01-02")]
		if !ok || d.OnlineSec < streakFrac*86400 {
			break
		}
		n++
	}
	return n
}

func (s *Stats) badges(ms *MinerStats, base Alert, now time.Time, net netdata.Snapshot) []Alert {
	var out []Alert
	streak := ms.Streak(now)
	for _, b := range badgeDefs {
		if ms.Badges[b.id] != 0 {
			continue
		}
		if p, _ := b.prog(ms, streak, net); p >= 1 {
			ms.Badges[b.id] = now.Unix()
			al := base
			al.Kind, al.Level, al.Title, al.Body = "badge", "good", "Badge earned: "+b.title, b.desc
			out = append(out, al)
		}
	}
	return out
}

// View is what the lottery card needs for one miner.
type StatsView struct {
	Leaderboard []ShareRec `json:"leaderboard"`
	TicketsAll  float64    `json:"tickets_all"`
	TicketsDay  float64    `json:"tickets_today"`
	ExpectedAll float64    `json:"expected_all"`
	ExpectedDay float64    `json:"expected_today"`
	Today       DayRec     `json:"today"`
	TodayAvgTHs float64    `json:"today_avg_ths"`
	TodayUpPct  float64    `json:"today_uptime_pct"`
	Blocks      []BlockRec `json:"blocks"`
	Badges      []Badge    `json:"badges"`
	Streak      int        `json:"streak_days"`
	Summaries   []Summary  `json:"summaries"`
	FirstSeen   int64      `json:"first_seen"`
}

func (s *Stats) View(id string, net netdata.Snapshot) StatsView {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.get(id)
	v := StatsView{Leaderboard: append([]ShareRec(nil), ms.Leaderboard...), TicketsAll: ms.TicketsAll, ExpectedAll: ms.ExpectedAll,
		Blocks: append([]BlockRec(nil), ms.Blocks...), Summaries: append([]Summary(nil), ms.Summaries...), FirstSeen: ms.FirstSeen}
	if n := len(ms.Days); n > 0 && ms.Days[n-1].Date == now.Format("2006-01-02") {
		v.Today = ms.Days[n-1]
	}
	v.TicketsDay, v.ExpectedDay = v.Today.Hashes, v.Today.Expected
	if v.Today.OnlineSec > 0 {
		v.TodayAvgTHs = v.Today.Hashes / v.Today.OnlineSec / 1e12
	}
	if v.Today.SeenSec > 0 {
		v.TodayUpPct = math.Min(100, v.Today.OnlineSec/v.Today.SeenSec*100)
	}
	if !Optional.Badges { // OPTIONAL:badges
		return v
	}
	v.Streak = ms.Streak(now)
	for _, b := range badgeDefs {
		p, hint := b.prog(ms, v.Streak, net)
		bd := Badge{ID: b.id, Title: b.title, Desc: b.desc, Icon: b.icon, Earned: ms.Badges[b.id], Progress: math.Max(0, math.Min(1, p)), Hint: hint}
		if bd.Earned != 0 {
			bd.Progress = 1
		}
		v.Badges = append(v.Badges, bd)
	}
	return v
}

// ---------------- number words ----------------

func siDiff(v float64) string {
	u := []string{"", "K", "M", "G", "T", "P", "E"}
	i := 0
	for math.Abs(v) >= 1000 && i < len(u)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f%s", v, u[i])
}

func siCount(v float64) string {
	u := []string{"", " thousand", " million", " billion", " trillion", " quadrillion", " quintillion", " sextillion", " septillion"}
	i := 0
	for math.Abs(v) >= 1000 && i < len(u)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f%s", v, u[i])
}

func pctText(f float64) string {
	p := f * 100
	switch {
	case p == 0:
		return "0%"
	case p >= 1:
		return fmt.Sprintf("%.2f%%", p)
	default:
		// enough significant digits to be non-zero
		d := int(math.Ceil(-math.Log10(p))) + 1
		return fmt.Sprintf("%.*f%%", d, p)
	}
}
