// Package sim is a small simulated Avalon Nano 3 CGMiner API (port 4028) for
// development and tests. It answers read commands with data captured from a real
// Nano 3 and applies workmode, ledset, switchpool, setpool and reboot changes
// to its in-memory state.
package sim

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Miner struct {
	mu        sync.Mutex
	WorkLevel int
	LED       [6]int // effect, bright, temper, r, g, b
	ReportLED bool   // include LEDUser[...] like the Nano 3S (the Nano 3 only reports Led[n])
	Received  []string
	start     time.Time
	// Test knobs (see Admin): found blocks, an offline miner, a dead pool,
	// a hashrate multiplier and extra chip heat.
	Blocks     int
	Offline    bool
	PoolDown   bool // the main pool (0) stops answering
	Rejecting  bool // the active pool reports Status=Rejecting and a high reject rate
	HashFactor float64
	ExtraHeat  int
	Target     int // TarT the miner reports (0 = 90)
	// Best share: simulated like a real miner. A miner at H hashes/s submits
	// H/2^32 difficulty-1 shares per second, and the best of n such shares is
	// -n/ln(U) (U uniform), so the best share grows roughly with uptime.
	Best     float64
	lastBest time.Time
	Active   int       // active pool
	spikeEnd time.Time // temporary heat spike
	spikeC   int
	conc     int // open API connections right now
	MaxConc  int // most connections ever open at once
	Pools    [3]poolInfo
	pending  [3]*poolInfo
	Reboots  int
	// Nano3S identifies as an Avalon Nano 3S so HashWatch offers Fan control.
	Nano3S bool
	// FanSpd is the last fan-spd value (-1 auto, or a fixed percent). FanSet is whether one was sent.
	FanSpd int
	FanSet bool
	// Share / block knobs for the Mining view (tests and screenshots).
	LastShareDiff float64
	WorkDiff      float64
	BlockHeight   int64
	extraAccept   int
	lastShareAt   int64
}

type poolInfo struct{ URL, User, Pass string }

func defaultPools() [3]poolInfo {
	return [3]poolInfo{
		{"stratum+tcp://solo.ckpool.org:3333", "bc1qsimulatedminer000000000000000000000.Nano3", "x"},
		{"stratum+tcp://public-pool.io:21496", "bc1qsimulatedminer000000000000000000000", "x"},
		{"stratum+tcp://solo.ckpool.org:3333", "bc1qsimulatedminer000000000000000000000.backup", "x"},
	}
}

func New() *Miner {
	m := &Miner{HashFactor: 1, WorkLevel: 2, LED: [6]int{3, 82, 50, 255, 106, 0}, start: time.Now().Add(-549866 * time.Second), Pools: defaultPools(),
		WorkDiff: 3997, LastShareDiff: 3997, BlockHeight: 975731}
	m.Best = m.bestOf(549866)
	m.lastBest = time.Now()
	return m
}

func (m *Miner) acceptedLocked() int {
	el := int(time.Since(m.start).Seconds())
	return 134043 + el/4 + m.extraAccept
}

func (m *Miner) lastShareUnixLocked() int64 {
	if m.lastShareAt > 0 {
		return m.lastShareAt
	}
	return time.Now().Unix() - 3
}

// AddShares pretends n accepted shares arrived, the latest at difficulty diff.
func (m *Miner) AddShares(n int, diff float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 1 {
		n = 1
	}
	m.extraAccept += n
	if diff > 0 {
		m.LastShareDiff = diff
		if diff > m.Best {
			m.Best = diff
		}
	}
	m.lastShareAt = time.Now().Unix()
}

// SetBlockHeight sets the pool's Current Block Height.
func (m *Miner) SetBlockHeight(h int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BlockHeight = h
}

// bestOf samples the best share of secs seconds of hashing.
func (m *Miner) bestOf(secs float64) float64 {
	n := m.hs() / 4294967296 * secs
	if n <= 0 {
		return 0
	}
	u := rand.Float64()
	for u == 0 {
		u = rand.Float64()
	}
	return math.Round(-n / math.Log(u))
}

func (m *Miner) hs() float64 { return []float64{3.4, 3.7, 3.95}[m.WorkLevel] * m.HashFactor * 1e12 }

func (m *Miner) advance() {
	now := time.Now()
	if b := m.bestOf(now.Sub(m.lastBest).Seconds()); b > m.Best {
		m.Best = b
	}
	m.lastBest = now
}

// Listen serves the API until the listener is closed.
func (m *Miner) Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go m.handle(c)
		}
	}()
	return ln, nil
}

func (m *Miner) handle(c net.Conn) {
	defer c.Close()
	m.mu.Lock()
	m.conc++
	m.MaxConc = max(m.MaxConc, m.conc)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.conc--; m.mu.Unlock() }()
	time.Sleep(2 * time.Millisecond) // a real miner isn't instant; makes overlap visible
	m.mu.Lock()
	off := m.Offline
	m.mu.Unlock()
	if off {
		return
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	n, _ := c.Read(buf)
	var req struct{ Command, Parameter string }
	raw := strings.TrimSpace(string(buf[:n]))
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		parts := strings.SplitN(raw, "|", 2) // plain-text form: cmd|param
		req.Command = parts[0]
		if len(parts) == 2 {
			req.Parameter = parts[1]
		}
	}
	m.mu.Lock()
	m.Received = append(m.Received, strings.TrimSpace(req.Command+"|"+req.Parameter))
	m.mu.Unlock()
	c.Write([]byte(m.reply(req.Command, req.Parameter) + "\x00"))
}

func status(st string, code int, msg string) string {
	return fmt.Sprintf(`{"STATUS":[{"STATUS":"%s","When":%d,"Code":%d,"Msg":%q,"Description":"cgminer 4.11.1"}]`, st, time.Now().Unix(), code, msg)
}

func (m *Miner) reply(cmd, param string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := rand.Float64()*0.4 - 0.2
	el := int(time.Since(m.start).Seconds())
	base := []float64{3.4, 3.7, 3.95}[m.WorkLevel] * m.HashFactor
	m.advance()
	heat := m.ExtraHeat
	if time.Now().Before(m.spikeEnd) {
		heat += m.spikeC
	}
	alive0, rej0 := "Alive", 0.1638
	if m.PoolDown {
		alive0 = "Dead"
	} else if m.Rejecting && m.Active == 0 {
		alive0, rej0 = "Rejecting", 62.5
	}
	act := func(n int) string {
		if n == m.Active && !(n == 0 && m.PoolDown) {
			return "true"
		}
		return "false"
	}
	watts := []int{85, 105, 126}[m.WorkLevel]
	switch cmd {
	case "version":
		if m.Nano3S {
			return status("S", 22, "CGMiner versions") + `,"VERSION":[{"CGMiner":"4.11.1","API":"3.7","PROD":"Avalon Nano3s","MODEL":"Nano3s","HWTYPE":"N_MM1v1_X1","SWTYPE":"MM319","LVERSION":"25061101_97e23a6","MAC":"e0e1a9000000"}],"id":1}`
		}
		return status("S", 22, "CGMiner versions") + `,"VERSION":[{"CGMiner":"4.11.1","API":"3.7","PROD":"Avalonnano","MODEL":"nano3","HWTYPE":"PMMv1_X1","SWTYPE":"MM318_X2","LVERSION":"25103101_0736b2e","MAC":"e0e1a9000000"}],"id":1}`
	case "summary":
		return status("S", 11, "Summary") + fmt.Sprintf(`,"SUMMARY":[{"Elapsed":%d,"MHS av":3221871.73,"MHS 5s":%.2f,"MHS 1m":%.2f,"MHS 5m":%.2f,"MHS 15m":%.2f,"Found Blocks":%d,"Getworks":18886,"Accepted":%d,"Rejected":233,"Hardware Errors":385,"Utility":14.63,"Stale":2627,"Get Failures":32,"Remote Failures":24,"Network Blocks":770,"Best Share":%.0f,"Device Hardware%%":0.0001,"Pool Rejected%%":0.1638,"Pool Stale%%":0.5190}],"id":1}`,
			el, (base+j*2)*1e6, (base+j)*1e6, (base+j/3)*1e6, base*1e6, m.Blocks, m.acceptedLocked(), m.Best)
	case "pools":
		wd, ld, bh := m.WorkDiff, m.LastShareDiff, m.BlockHeight
		if wd <= 0 {
			wd = 3997
		}
		if ld <= 0 {
			ld = wd
		}
		if bh <= 0 {
			bh = 975731
		}
		return status("S", 7, "3 Pool(s)") + fmt.Sprintf(`,"POOLS":[{"POOL":0,"URL":"%s","Status":"%s","Priority":0,"Quota":1,"User":"%s","Accepted":%d,"Rejected":233,"Stale":2627,"Last Share Time":%d,"Stratum Active":%s,"Stratum URL":"%s","Best Share":%.0f,"Pool Rejected%%":%.4f,"Pool Stale%%":0.5190,"Work Difficulty":%.1f,"Last Share Difficulty":%.4f,"Current Block Height":%d,"Get Failures":32,"Remote Failures":24},{"POOL":1,"URL":"%s","Status":"Alive","Priority":1,"Quota":1,"User":"%s","Stratum Active":%s,"Accepted":0,"Work Difficulty":1024.0},{"POOL":2,"URL":"%s","Status":"Alive","Priority":2,"Quota":1,"User":"%s","Stratum Active":%s,"Accepted":0}],"id":1}`,
			m.Pools[0].URL, alive0, m.Pools[0].User, m.acceptedLocked(), m.lastShareUnixLocked(), act(0), poolHost(m.Pools[0].URL), m.Best, rej0, wd, ld, bh, m.Pools[1].URL, m.Pools[1].User, act(1), m.Pools[2].URL, m.Pools[2].User, act(2))
	case "devs":
		return status("S", 9, "1 ASC(s)") + `,"DEVS":[{"ASC":0,"Name":"AVANANO","ID":0,"Enabled":"Y","Status":"Alive"}],"id":1}`
	case "estats", "stats":
		led := ""
		if m.ReportLED {
			led = fmt.Sprintf(" LEDUser[%d-%d-%d-%d-%d-%d]", m.LED[0], m.LED[1], m.LED[2], m.LED[3], m.LED[4], m.LED[5])
		}
		ver, hw, core, ps := "nano3-25103101_0736b2e", "25103101_b906c52_1733f44", "A3198S", fmt.Sprintf("0 0 0 4 2756 %d 328", watts)
		if m.Nano3S {
			ver, hw, core, ps = "Nano3s-25061101_97e23a6", "25061101_97e23a6", "A3197S", fmt.Sprintf("0 0 27482 3 0 3444 %d", watts)
		}
		fanR := 29 + 7*m.WorkLevel
		fan1 := 2100 + 525*m.WorkLevel
		if m.FanSet && m.FanSpd >= 40 {
			fanR = m.FanSpd
			fan1 = 16 * m.FanSpd
		}
		mm := fmt.Sprintf(`Ver[%s] HVer[%s] HashStatus[1] NETFAIL[%d] Elapsed[%d] HW[385] DH[1.827%%] Temp[41] OTemp[36] TMax[%d] TAvg[%d] TarT[%d] Fan1[%d] FanR[%d%%] PS[%s] GHSspd[%.2f] GHSmm[4241.60] GHSavg[3227.08] WU[45081.76] Led[%d]%s TA[10] Core[%s] PING[138] SoftOFF[0] ECHU[0] ECMM[0] PVT_T0[88  91  91  89  88  88  92  91  91  87] PVT_V0[318 309 313 316 316 316 315 309 314 314] MW0[13874 13534 14249 13947 14149 13732 13750 13753 13782 13715] CRC[0] WORKLEVEL[%d] MPO[125]`,
			ver, hw, time.Now().Unix()-86400*4, el, 84+3*m.WorkLevel+heat, 82+3*m.WorkLevel+heat, cmp.Or(m.Target, 90), fan1, fanR, ps, base*1000+j*100, m.LED[0], led, core, m.WorkLevel)
		return status("S", 70, "CGMiner stats") + `,"STATS":[{"STATS":0,"ID":"AVANANO0","Elapsed":` + strconv.Itoa(el) + `,"MM ID0":"` + mm + `"}],"id":1}`
	case "ascset":
		p := strings.Split(param, ",")
		if len(p) >= 4 && p[0] == "0" && p[1] == "workmode" && p[2] == "set" {
			if n, err := strconv.Atoi(p[3]); err == nil && n >= 0 && n <= 2 {
				m.WorkLevel = n
				return status("S", 119, "ASC 0 set OK") + `,"id":1}`
			}
		}
		if len(p) == 3 && p[0] == "0" && p[1] == "ledset" {
			f := strings.Split(p[2], "-")
			if len(f) == 6 {
				var v [6]int
				for i, x := range f {
					v[i], _ = strconv.Atoi(x)
				}
				m.LED = v
				return status("I", 118, "ASC 0 set info: led set ok") + `,"id":1}`
			}
		}
		if len(p) == 3 && p[0] == "0" && p[1] == "fan-spd" {
			n, err := strconv.Atoi(p[2])
			if err == nil && (n == -1 || (n >= 15 && n <= 100)) {
				m.FanSpd = n
				m.FanSet = true
				return status("S", 119, "ASC 0 set OK") + `,"id":1}`
			}
		}
		if len(p) == 3 && p[0] == "0" && p[1] == "reboot" && p[2] == "0" {
			for i, pend := range m.pending {
				if pend != nil {
					m.Pools[i] = *pend
					m.pending[i] = nil
				}
			}
			m.Reboots++
			return status("S", 119, "ASC 0 set OK") + `,"id":1}`
		}
		return status("E", 120, "ASC 0 set failed: invalid option") + `,"id":1}`
	case "setpool":
		parts := strings.Split(param, ",")
		if len(parts) != 6 {
			return status("E", 14, "Invalid setpool") + `,"id":1}`
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 || n > 2 {
			return status("E", 14, "Invalid pool number") + `,"id":1}`
		}
		m.pending[n] = &poolInfo{URL: parts[3], User: parts[4], Pass: parts[5]}
		msg := fmt.Sprintf("Pool set info:\npool %d success set to %s\nworker is  %s\nworkerpassword is %s\nPlease reboot miner to make config work.", n, parts[3], parts[4], parts[5])
		return status("S", 133, msg) + `,"id":1}`
	case "switchpool":
		n, err := strconv.Atoi(param)
		if err != nil || n < 0 || n > 2 {
			return status("E", 14, "Invalid pool number") + `,"id":1}`
		}
		m.Active = n
		return status("S", 7, fmt.Sprintf("Switching to pool %d", n)) + `,"id":1}`
	}
	return status("E", 14, "Invalid command") + `,"id":1}`
}

// Concurrency returns the most API connections that were ever open at once (and resets it).
func (m *Miner) Concurrency() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.MaxConc
	m.MaxConc = m.conc
	return n
}

// Log returns the commands received so far.
func (m *Miner) Log() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.Received...)
}

// Admin serves test knobs over HTTP (for development only):
//
//	GET /block        found blocks +1
//	GET /offline?on=1 stop answering the API (on=0 to resume)
//	GET /pool?down=1  report no active pool
//	GET /pool?reject=1  the active pool is rejecting shares
//	GET /hash?f=0.5   multiply the hashrate
//	GET /heat?c=6     add degrees to the chip temperatures
//	GET /spike?c=9&s=300  add degrees for s seconds (a temperature spike)
//	GET /best?d=2.1e9 the miner reports a new best share of difficulty d
//	GET /share?n=1&d=12000  n new accepted shares, latest difficulty d
//	GET /height?h=977086    set Current Block Height (or add=1 to increment)
//
// SetElapsed pretends the miner has been up for sec seconds (tests: a reboot is a drop).
func (m *Miner) SetElapsed(sec int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.start = time.Now().Add(-time.Duration(sec) * time.Second)
}

func (m *Miner) Admin(addr string) error {
	mux := http.NewServeMux()
	on := func(r *http.Request, k string) bool { return r.URL.Query().Get(k) != "0" }
	mux.HandleFunc("/block", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.Blocks++
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/offline", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.Offline = on(r, "on")
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/pool", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		if r.URL.Query().Has("down") {
			m.PoolDown = on(r, "down")
		}
		if r.URL.Query().Has("reject") {
			m.Rejecting = on(r, "reject")
		}
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/hash", func(w http.ResponseWriter, r *http.Request) {
		f, err := strconv.ParseFloat(r.URL.Query().Get("f"), 64)
		if err != nil || f <= 0 {
			f = 1
		}
		m.mu.Lock()
		m.HashFactor = f
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/heat", func(w http.ResponseWriter, r *http.Request) {
		c, _ := strconv.Atoi(r.URL.Query().Get("c"))
		m.mu.Lock()
		m.ExtraHeat = c
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/spike", func(w http.ResponseWriter, r *http.Request) {
		c, _ := strconv.Atoi(r.URL.Query().Get("c"))
		sec, _ := strconv.Atoi(r.URL.Query().Get("s"))
		if sec <= 0 {
			sec = 300
		}
		m.mu.Lock()
		m.spikeC, m.spikeEnd = c, time.Now().Add(time.Duration(sec)*time.Second)
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/best", func(w http.ResponseWriter, r *http.Request) {
		d, err := strconv.ParseFloat(r.URL.Query().Get("d"), 64)
		m.mu.Lock()
		if err == nil && d > m.Best {
			m.Best = d
		}
		m.mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/share", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		d, _ := strconv.ParseFloat(r.URL.Query().Get("d"), 64)
		m.AddShares(n, d)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/height", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("h") {
			h, _ := strconv.ParseInt(r.URL.Query().Get("h"), 10, 64)
			m.SetBlockHeight(h)
		} else if r.URL.Query().Get("add") != "" {
			add, _ := strconv.ParseInt(r.URL.Query().Get("add"), 10, 64)
			if add == 0 {
				add = 1
			}
			m.mu.Lock()
			m.BlockHeight += add
			m.mu.Unlock()
		}
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"work_level": m.WorkLevel, "led": m.LED, "active_pool": m.Active, "pool_down": m.PoolDown, "rejecting": m.Rejecting,
			"best": m.Best, "blocks": m.Blocks, "received": len(m.Received)})
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go http.Serve(ln, mux)
	return nil
}

func poolHost(u string) string {
	u = strings.TrimPrefix(u, "stratum+tcp://")
	u = strings.TrimPrefix(u, "stratum+ssl://")
	u = strings.TrimPrefix(u, "stratum+tls://")
	if i := strings.LastIndex(u, ":"); i > 0 {
		return u[:i]
	}
	return u
}
