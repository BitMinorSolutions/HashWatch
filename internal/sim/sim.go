// Package sim is a small simulated Avalon Nano 3 CGMiner API (port 4028) for
// development and tests. It answers read commands with data captured from a real
// Nano 3 and applies "ascset" workmode / ledset changes to its in-memory state.
package sim

import (
	"encoding/json"
	"fmt"
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
	PoolDown   bool
	HashFactor float64
	ExtraHeat  int
}

func New() *Miner {
	return &Miner{HashFactor: 1, WorkLevel: 2, LED: [6]int{3, 82, 50, 255, 106, 0}, start: time.Now().Add(-549866 * time.Second)}
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
	alive, active := "Alive", "true"
	if m.PoolDown {
		alive, active = "Dead", "false"
	}
	watts := []int{85, 105, 126}[m.WorkLevel]
	switch cmd {
	case "version":
		return status("S", 22, "CGMiner versions") + `,"VERSION":[{"CGMiner":"4.11.1","API":"3.7","PROD":"Avalonnano","MODEL":"nano3","HWTYPE":"PMMv1_X1","SWTYPE":"MM318_X2","LVERSION":"25103101_0736b2e","MAC":"e0e1a9000000"}],"id":1}`
	case "summary":
		return status("S", 11, "Summary") + fmt.Sprintf(`,"SUMMARY":[{"Elapsed":%d,"MHS av":3221871.73,"MHS 5s":%.2f,"MHS 1m":%.2f,"MHS 5m":%.2f,"MHS 15m":%.2f,"Found Blocks":%d,"Getworks":18886,"Accepted":%d,"Rejected":233,"Hardware Errors":385,"Utility":14.63,"Stale":2627,"Get Failures":32,"Remote Failures":24,"Network Blocks":770,"Best Share":423759812,"Device Hardware%%":0.0001,"Pool Rejected%%":0.1638,"Pool Stale%%":0.5190}],"id":1}`,
			el, (base+j*2)*1e6, (base+j)*1e6, (base+j/3)*1e6, base*1e6, m.Blocks, 134043+el/4)
	case "pools":
		return status("S", 7, "3 Pool(s)") + fmt.Sprintf(`,"POOLS":[{"POOL":0,"URL":"stratum+tcp://solo.ckpool.org:3333","Status":"%s","Priority":0,"Quota":1,"User":"bc1qsimulatedminer000000000000000000000.Nano3","Accepted":%d,"Rejected":233,"Stale":2627,"Last Share Time":%d,"Stratum Active":%s,"Stratum URL":"solo.ckpool.org","Best Share":423759812,"Pool Rejected%%":0.1638,"Pool Stale%%":0.5190,"Work Difficulty":3997.0,"Last Share Difficulty":3997.0,"Current Block Height":975731,"Get Failures":32,"Remote Failures":24},{"POOL":1,"URL":"stratum+tcp://backup.example:3333","Status":"Alive","Priority":1,"Quota":1,"User":"bc1qsimulatedminer000000000000000000000","Stratum Active":false,"Accepted":0}],"id":1}`,
			alive, 134043+el/4, time.Now().Unix()-3, active)
	case "devs":
		return status("S", 9, "1 ASC(s)") + `,"DEVS":[{"ASC":0,"Name":"AVANANO","ID":0,"Enabled":"Y","Status":"Alive"}],"id":1}`
	case "estats", "stats":
		led := ""
		if m.ReportLED {
			led = fmt.Sprintf(" LEDUser[%d-%d-%d-%d-%d-%d]", m.LED[0], m.LED[1], m.LED[2], m.LED[3], m.LED[4], m.LED[5])
		}
		mm := fmt.Sprintf(`Ver[nano3-25103101_0736b2e] HVer[25103101_b906c52_1733f44] HashStatus[1] NETFAIL[%d] Elapsed[%d] HW[385] DH[1.827%%] Temp[41] OTemp[36] TMax[%d] TAvg[%d] TarT[90] Fan1[%d] FanR[%d%%] PS[0 0 0 4 2756 %d 328] GHSspd[%.2f] GHSmm[4241.60] GHSavg[3227.08] WU[45081.76] Led[%d]%s TA[10] Core[A3198S] PING[138] SoftOFF[0] ECHU[0] ECMM[0] PVT_T0[88  91  91  89  88  88  92  91  91  87] PVT_V0[318 309 313 316 316 316 315 309 314 314] MW0[13874 13534 14249 13947 14149 13732 13750 13753 13782 13715] CRC[0] WORKLEVEL[%d] MPO[125]`,
			time.Now().Unix()-86400*4, el, 86+3*m.WorkLevel+m.ExtraHeat, 84+3*m.WorkLevel+m.ExtraHeat, 2100+525*m.WorkLevel, 29+7*m.WorkLevel, watts, base*1000+j*100, m.LED[0], led, m.WorkLevel)
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
		return status("E", 120, "ASC 0 set failed: invalid option") + `,"id":1}`
	}
	return status("E", 14, "Invalid command") + `,"id":1}`
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
//	GET /hash?f=0.5   multiply the hashrate
//	GET /heat?c=6     add degrees to the chip temperatures
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
		m.PoolDown = on(r, "down")
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
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go http.Serve(ln, mux)
	return nil
}
