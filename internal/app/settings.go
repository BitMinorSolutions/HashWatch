package app

import (
	"time"

	"github.com/hashwatch/hashwatch/internal/auto"
)

// MinerOpts are one miner's opt-in automations and light effects. Everything is
// off until the user switches it on; zero values mean "use the default".
type MinerOpts struct {
	Schedule ScheduleOpts `json:"schedule,omitempty"`
	Failover FailoverOpts `json:"failover,omitempty"`
	Light    LightOpts    `json:"light,omitempty"`
	Fan      FanOpts      `json:"fan,omitempty"`
}

// FanOpts is the user's chosen fan mode. Mode is "auto" (default) or "cooler".
// Percent is the fixed speed (40-100) when Cooler; 0 means the default 50%.
type FanOpts struct {
	Mode    string `json:"mode,omitempty"`
	Percent int    `json:"percent,omitempty"`
}

func (f FanOpts) Cooler() bool { return f.Mode == "cooler" }

func (f FanOpts) Speed() int {
	if f.Percent >= 40 && f.Percent <= 100 {
		return f.Percent
	}
	return 50
}

type ScheduleOpts struct {
	On      bool        `json:"on"`
	Default string      `json:"default,omitempty"` // speed outside every rule ("" = leave as is)
	Rules   []auto.Rule `json:"rules,omitempty"`
}

// LightOpts: the miner's light as a status lamp and for celebrations.
type FailoverOpts struct {
	On        bool `json:"on"`
	DeadMin   int  `json:"dead_min,omitempty"`   // wait before switching; 0 = 5
	CoolMin   int  `json:"cool_min,omitempty"`   // cooldown between switches; 0 = 5
	ReturnMin int  `json:"return_min,omitempty"` // wait before switching back; 0 = 15
}

func (f FailoverOpts) Cfg() auto.FailoverCfg {
	c := auto.FailoverCfg{
		DeadFor:     time.Duration(f.DeadMin) * time.Minute,
		Cooldown:    time.Duration(f.CoolMin) * time.Minute,
		ReturnAfter: time.Duration(f.ReturnMin) * time.Minute,
	}
	if c.DeadFor == 0 {
		c.DeadFor = 5 * time.Minute
	}
	if c.Cooldown == 0 {
		c.Cooldown = 5 * time.Minute
	}
	if c.ReturnAfter == 0 {
		c.ReturnAfter = 15 * time.Minute
	}
	return c
}

type LightOpts struct {
	Lamp      bool        `json:"lamp"`           // green healthy, red problem
	Celebrate bool        `json:"celebrate"`      // flash gold when a block is found
	BestFlash bool        `json:"best_flash"`     // short flash on a new personal-best share
	Base      *LightState `json:"base,omitempty"` // the user's own light, restored afterwards
}

type LightState struct {
	Mode   string `json:"mode"`  // off fixed flash pulse loop
	Color  string `json:"color"` // #rrggbb
	Bright int    `json:"brightness"`
}

// SecuritySettings: 2FA for remote sign-ins and view-only access.
type SecuritySettings struct {
	ViewHash string `json:"view_hash,omitempty"`
	ViewSalt string `json:"view_salt,omitempty"`
	ViewLink string `json:"view_link,omitempty"` // secret for /v/<token>; empty = off
}

// PrivacySettings: every outside call can be switched off.
type PrivacySettings struct {
	NetworkOff bool `json:"network_off,omitempty"` // difficulty, tip height, recent blocks (mempool.space)
	PriceOff   bool `json:"price_off,omitempty"`   // BTC/USD price (mempool.space)
}

// FunSettings: lottery extras.
type FunSettings struct {
	SoundOff         bool    `json:"sound_off,omitempty"`
	NearMissDiff     float64 `json:"near_miss_diff,omitempty"` // alert for any share at or above this difficulty (0 = personal bests only)
	Summary          string  `json:"summary,omitempty"`        // "daily" (default), "weekly", "off"
	SummaryWeekday   int     `json:"summary_weekday,omitempty"`
	MenuSparklineOff bool    `json:"menu_sparkline_off,omitempty"`
}

// Location is a V2 site (another home, a shop…) that miners can be grouped under.
type Location struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PushTarget is a V2 phone push registration (APNs / Web Push). Unused in v0.5.x.
type PushTarget struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "webpush" | "apns"
	Endpoint string `json:"endpoint"`
	Name     string `json:"name"`
	Added    int64  `json:"added"`
}

// minerByID returns a copy of a miner's settings.
func (c *Config) minerByID(id string) (Miner, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.Miners {
		if m.ID == id {
			return m, true
		}
	}
	return Miner{}, false
}

// updateMiner edits one miner's settings under the lock.
func (c *Config) updateMiner(id string, f func(*Miner)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Miners {
		if c.Miners[i].ID == id {
			f(&c.Miners[i])
			return true
		}
	}
	return false
}
