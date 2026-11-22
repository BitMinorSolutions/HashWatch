// Package model is the miner snapshot shared by every data source and the UI.
// Field names intentionally mirror the Avalon Nano 3 dashboard JSON so both
// the direct CGMiner (port 4028) source and the remote/demo source map 1:1.
package model

type Snapshot struct {
	OK          bool              `json:"ok"`
	FetchedAt   int64             `json:"fetched_at_unix"`
	MinerHost   string            `json:"miner_host"`
	Source      string            `json:"source"`
	QueryErrors []string          `json:"query_errors"`
	Identity    Identity          `json:"identity"`
	Hashrate    Hashrate          `json:"hashrate"`
	Temps       Temps             `json:"temps"`
	Fan         Fan               `json:"fan"`
	Power       Power             `json:"power"`
	Uptime      Uptime            `json:"uptime"`
	Shares      Shares            `json:"shares"`
	Pools       []Pool            `json:"pools"`
	Chips       Chips             `json:"chips"`
	Errors      Errors            `json:"errors"`
	Device      Device            `json:"device"`
	Control     *Control          `json:"control,omitempty"`
	Warnings    []string          `json:"warnings"`
	MMFields    map[string]string `json:"mm_fields"`
}

type Identity struct {
	Product  string `json:"product,omitempty"`
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Hardware string `json:"hardware,omitempty"`
	CGMiner  string `json:"cgminer,omitempty"`
	API      string `json:"api,omitempty"`
	HWType   string `json:"hwtype,omitempty"`
	SWType   string `json:"swtype,omitempty"`
	Core     string `json:"core,omitempty"`
	Chips    int    `json:"chips,omitempty"`
	ID       string `json:"id,omitempty"`
	MAC      string `json:"mac,omitempty"`
}

type Hashrate struct {
	THsHero *float64 `json:"ths_hero"`
	THs5s   *float64 `json:"ths_5s"`
	THs1m   *float64 `json:"ths_1m"`
	THs5m   *float64 `json:"ths_5m"`
	THs15m  *float64 `json:"ths_15m"`
	THsAvg  *float64 `json:"ths_avg"`
	GHsMM   *float64 `json:"ghs_mm"`
	GHsAvg  *float64 `json:"ghs_avg"`
	GHsSpd  *float64 `json:"ghs_spd"`
	WU      *float64 `json:"wu"`
}

type Temps struct {
	Ambient *float64 `json:"ambient"`
	Outlet  *float64 `json:"outlet"`
	Avg     *float64 `json:"avg"`
	Max     *float64 `json:"max"`
	Target  *float64 `json:"target"`
}

type Fan struct {
	RPM     *float64 `json:"rpm"`
	Percent *float64 `json:"percent"`
}

type Power struct {
	Watts     *float64  `json:"watts"`
	VoltageV  *float64  `json:"voltage_v"`
	MPOW      *float64  `json:"mpo_w"`
	PSRaw     []float64 `json:"ps_raw"`
	PSOK      *bool     `json:"ps_ok"`
	WorkLevel *int      `json:"work_level"`
}

type Uptime struct {
	Seconds int64  `json:"seconds"`
	Human   string `json:"human"`
}

type Shares struct {
	Accepted       *float64 `json:"accepted"`
	Rejected       *float64 `json:"rejected"`
	Stale          *float64 `json:"stale"`
	HWErrors       *float64 `json:"hw_errors"`
	RejectPct      *float64 `json:"reject_pct"`
	StalePct       *float64 `json:"stale_pct"`
	DeviceHWPct    *float64 `json:"device_hw_pct"`
	DHPct          *float64 `json:"dh_pct"`
	BestShare      *float64 `json:"best_share"`
	Utility        *float64 `json:"utility"`
	Getworks       *float64 `json:"getworks"`
	GetFailures    *float64 `json:"get_failures"`
	RemoteFailures *float64 `json:"remote_failures"`
	FoundBlocks    *float64 `json:"found_blocks"`
	NetworkBlocks  *float64 `json:"network_blocks"`
}

type Pool struct {
	ID             int      `json:"id"`
	URL            string   `json:"url"`
	StratumURL     string   `json:"stratum_url"`
	Status         string   `json:"status"`
	Priority       int      `json:"priority"`
	User           string   `json:"user"`
	StratumActive  bool     `json:"stratum_active"`
	Accepted       *float64 `json:"accepted"`
	Rejected       *float64 `json:"rejected"`
	Stale          *float64 `json:"stale"`
	RejectPct      *float64 `json:"reject_pct"`
	StalePct       *float64 `json:"stale_pct"`
	Difficulty     *float64 `json:"difficulty"`
	LastShareDiff  *float64 `json:"last_share_diff,omitempty"`
	WorkDifficulty *float64 `json:"work_difficulty,omitempty"`
	LastShareUnix  int64    `json:"last_share_unix"`
	BestShare      *float64 `json:"best_share"`
	BlockHeight    int64    `json:"block_height"`
	GetFailures    *float64 `json:"get_failures"`
	RemoteFailures *float64 `json:"remote_failures"`
}

type Chips struct {
	Temps   []float64 `json:"temps"`
	VoltsMV []float64 `json:"volts_mv"`
	Nonces  []float64 `json:"nonces"`
}

type NetFail struct {
	Unix int64 `json:"unix"`
}

type Errors struct {
	ECHU       []float64 `json:"echu"`
	ECMM       *float64  `json:"ecmm"`
	HashStatus *int      `json:"hash_status"`
	SoftOff    *float64  `json:"soft_off"`
	CRC        *float64  `json:"crc"`
	NetFail    []NetFail `json:"net_fail"`
	PingMS     *float64  `json:"ping_ms"`
	LED        string    `json:"led"`
}

type Device struct {
	Name    string `json:"name"`
	Enabled string `json:"enabled"`
	Status  string `json:"status"`
}

// Control is the work-mode / light state read back from the miner.
type Control struct {
	LEDTemper *int   `json:"led_temper,omitempty"`
	WorkLevel *int   `json:"work_level"`
	WorkName  string `json:"work_name"`
	LEDMode   string `json:"led_mode"`
	LEDHex    string `json:"led_hex"`
	LEDBright *int   `json:"led_brightness"`
}

// Sample is one point of the in-app history used by the charts.
type Sample struct {
	T     int64    `json:"t"`
	H5s   *float64 `json:"h5s"`
	H1m   *float64 `json:"h1m"`
	H5m   *float64 `json:"h5m"`
	H15m  *float64 `json:"h15m"`
	HHero *float64 `json:"hhero,omitempty"`
	TMax  *float64 `json:"tmax"`
	TAvg  *float64 `json:"tavg"`
	TAmb  *float64 `json:"tamb"`
	TOut  *float64 `json:"tout"`
	RPM   *float64 `json:"rpm"`
	W     *float64 `json:"w"`
	Acc   *float64 `json:"acc"`
	Rej   *float64 `json:"rej"`
	HW    *float64 `json:"hw"`
}

func (s *Snapshot) Sample() Sample {
	return Sample{T: s.FetchedAt, H5s: s.Hashrate.THs5s, H1m: s.Hashrate.THs1m, H5m: s.Hashrate.THs5m, H15m: s.Hashrate.THs15m, HHero: s.Hashrate.THsHero,
		TMax: s.Temps.Max, TAvg: s.Temps.Avg, TAmb: s.Temps.Ambient, TOut: s.Temps.Outlet, RPM: s.Fan.RPM, W: s.Power.Watts,
		Acc: s.Shares.Accepted, Rej: s.Shares.Rejected, HW: s.Shares.HWErrors}
}
