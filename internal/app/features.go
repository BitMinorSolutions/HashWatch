package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/hashwatch/hashwatch/internal/auto"
	"github.com/hashwatch/hashwatch/internal/lottery"
	"github.com/hashwatch/hashwatch/internal/netdata"
)

// netView is the network data the UI may show, honouring the privacy switches.
func (s *Server) netView() (netdata.Snapshot, map[string]any) {
	var n netdata.Snapshot
	p := s.Cfg.Privacy
	info := map[string]any{"network_on": !p.NetworkOff && s.Net != nil, "price_on": !p.PriceOff && s.Net != nil}
	if s.Net == nil {
		return n, info
	}
	full := s.Net.Get()
	now := s.Now().Unix()
	if !p.NetworkOff && full.NetFetched > 0 {
		n.Difficulty, n.TipHeight, n.Blocks, n.AvgFeesSat, n.NetFetched, n.Source = full.Difficulty, full.TipHeight, full.Blocks, full.AvgFeesSat, full.NetFetched, full.Source
		info["network_age"] = now - full.NetFetched
		info["network_stale"] = now-full.NetFetched > 30*60
	}
	if !p.NetworkOff && full.LastErr != "" {
		info["network_err"] = full.LastErr
	}
	if !p.PriceOff && full.PriceFetched > 0 {
		n.PriceUSD, n.PriceFetched, n.PriceSource = full.PriceUSD, full.PriceFetched, full.PriceSource
		info["price_age"] = now - full.PriceFetched
		info["price_stale"] = now-full.PriceFetched > 60*60
	}
	if !p.PriceOff && full.PriceErr != "" {
		info["price_err"] = full.PriceErr
	}
	return n, info
}

// NetForStats is the network data used for stats (respects privacy).
func (s *Server) NetForStats() netdata.Snapshot {
	if s.Now == nil {
		return netdata.Snapshot{}
	}
	n, _ := s.netView()
	return n
}

func (s *Server) lotteryGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("miner")
	if _, ok := s.Cfg.minerByID(id); !ok {
		writeJSON(w, 404, map[string]string{"error": "unknown miner"})
		return
	}
	net, info := s.netView()
	out := map[string]any{"net": info, "server_time": s.Now().Unix()}
	snap := s.Poll.Latest(id)
	var h, sessionBest float64
	if snap != nil && snap.OK {
		h = oddsHashrate(snap)
		sessionBest, _ = fnum(snap.Shares.BestShare)
		if avg, ok := fnum(snap.Hashrate.THsAvg); ok && snap.Uptime.Seconds > 0 {
			out["tickets_since_boot"] = avg * 1e12 * float64(snap.Uptime.Seconds)
		}
	}
	var sv StatsView
	if s.Stats != nil {
		sv = s.Stats.View(id, net)
		out["stats"] = sv
	}
	best := sessionBest
	if len(sv.Leaderboard) > 0 && sv.Leaderboard[0].Diff > best {
		best = sv.Leaderboard[0].Diff
	}
	out["odds"] = lottery.Compute(h, net.Difficulty, best)
	out["session_best"] = sessionBest
	if net.TipHeight > 0 {
		out["reward"] = lottery.BlockReward(net.TipHeight, net.AvgFeesSat, net.PriceUSD)
		out["tip_height"] = net.TipHeight
	}
	if net.Difficulty > 0 {
		out["difficulty"] = net.Difficulty
	}
	if net.PriceUSD > 0 {
		out["price_usd"] = net.PriceUSD
	}
	blocks := net.Blocks
	if len(blocks) > 12 {
		blocks = blocks[:12]
	}
	out["blocks"] = blocks
	writeJSON(w, 200, out)
}

func (s *Server) featuresGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("miner")
	m, ok := s.Cfg.minerByID(id)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "unknown miner"})
		return
	}
	s.Cfg.mu.Lock()
	fun, priv := s.Cfg.Fun, s.Cfg.Privacy
	s.Cfg.mu.Unlock()
	if fun.Summary == "" {
		fun.Summary = "daily"
	}
	o := m.Opts
	target := 0.0
	if snap := s.Poll.Latest(id); snap != nil && snap.OK {
		target, _ = fnum(snap.Temps.Target)
	}
	hot, fromMiner := auto.HotPoint(target)
	out := map[string]any{"opts": o, "fun": fun, "privacy": priv, "can_control": s.canControl(id) && s.role(r) == RoleAdmin,
		"heat": map[string]any{"hot_c": hot, "from_miner": fromMiner, "fallback_c": auto.FallbackHotC},
	}
	now := s.Now()
	if mode, rule := auto.Desired(o.Schedule.Rules, o.Schedule.Default, now); o.Schedule.On {
		out["schedule_now"] = map[string]any{"mode": mode, "rule": rule}
	}
	if s.Auto != nil {
		out["auto"] = s.Auto.View(id)
	}
	writeJSON(w, 200, out)
}

func (s *Server) featuresSet(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Miner    string
		Schedule *ScheduleOpts
		Light    *struct{ Lamp, Celebrate, BestFlash *bool }
		Fun      *FunSettings
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	minerOpts := b.Schedule != nil || b.Light != nil
	if minerOpts && !s.canControl(b.Miner) {
		writeJSON(w, 403, map[string]string{"error": ErrNoControl.Error()})
		return
	}
	bad := func(msg string) { writeJSON(w, 400, map[string]string{"error": msg}) }
	if sc := b.Schedule; sc != nil {
		sc.Default = strings.ToLower(sc.Default)
		if len(sc.Rules) > 24 {
			bad("Up to 24 schedule rules.")
			return
		}
		for i := range sc.Rules {
			sc.Rules[i].Mode = strings.ToLower(sc.Rules[i].Mode)
			if err := sc.Rules[i].Validate(); err != nil {
				bad("Rule " + itoa(i+1) + ": " + err.Error())
				return
			}
		}
		if _, ok := auto.LevelOf(sc.Default); sc.Default != "" && !ok {
			bad("Default speed must be low, medium, high or empty.")
			return
		}
	}
	if f := b.Fun; f != nil {
		if f.Summary != "" && f.Summary != "daily" && f.Summary != "weekly" && f.Summary != "off" {
			bad("Summary must be daily, weekly or off.")
			return
		}
		if f.NearMissDiff < 0 || f.SummaryWeekday < 0 || f.SummaryWeekday > 6 {
			bad("Bad lottery settings.")
			return
		}
	}
	if minerOpts {
		snap := s.Poll.Latest(b.Miner)
		s.Cfg.updateMiner(b.Miner, func(m *Miner) {
			if b.Schedule != nil {
				m.Opts.Schedule = *b.Schedule
			}
			if l := b.Light; l != nil {
				if l.Lamp != nil {
					m.Opts.Light.Lamp = *l.Lamp
				}
				if l.Celebrate != nil {
					m.Opts.Light.Celebrate = *l.Celebrate
				}
				if l.BestFlash != nil {
					m.Opts.Light.BestFlash = *l.BestFlash
				}
				// remember the user's own light so effects can put it back
				if m.Opts.Light.Base == nil && snap != nil && snap.Control != nil && snap.Control.LEDHex != "" {
					c := snap.Control
					br := 100
					if c.LEDBright != nil {
						br = *c.LEDBright
					}
					mode := c.LEDMode
					if mode == "" {
						mode = "fixed"
					}
					m.Opts.Light.Base = &LightState{Mode: mode, Color: c.LEDHex, Bright: br}
				}
			}
		})
		if b.Schedule != nil && s.Auto != nil {
			s.Auto.ResetSchedule(b.Miner)
		}
	}
	if b.Fun != nil {
		s.Cfg.mu.Lock()
		s.Cfg.Fun = *b.Fun
		s.Cfg.mu.Unlock()
	}
	if err := s.Cfg.Save(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	s.featuresGet(w, withMiner(r, b.Miner))
}

func withMiner(r *http.Request, id string) *http.Request {
	r2 := r.Clone(r.Context())
	q := r2.URL.Query()
	q.Set("miner", id)
	r2.URL.RawQuery = q.Encode()
	return r2
}

func itoa(n int) string { return strconv.Itoa(n) }

func (s *Server) privacySet(w http.ResponseWriter, r *http.Request) {
	var b struct{ NetworkOff, PriceOff *bool }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b)
	s.Cfg.mu.Lock()
	if b.NetworkOff != nil {
		s.Cfg.Privacy.NetworkOff = *b.NetworkOff
	}
	if b.PriceOff != nil {
		s.Cfg.Privacy.PriceOff = *b.PriceOff
	}
	p := s.Cfg.Privacy
	s.Cfg.mu.Unlock()
	s.Cfg.Save()
	if s.Net != nil {
		s.Net.Kick()
	}
	writeJSON(w, 200, map[string]any{"ok": true, "privacy": p})
}
