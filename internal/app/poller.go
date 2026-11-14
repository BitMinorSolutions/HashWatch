package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/source"
)

const historyMax = 24 * 3600 / 15 // 24h at 15 s

type minerState struct {
	Latest  *model.Snapshot `json:"latest"`
	LastOK  int64           `json:"last_ok"`
	LastErr string          `json:"last_err"`
	History []model.Sample  `json:"history"`
	src     source.Source
	stop    chan struct{}
	ctlMu   sync.Mutex // one control command at a time per miner
	lastCtl time.Time
	ledMemo *model.Control // last light settings we applied (for firmwares that don't report them)
}

type Poller struct {
	Every time.Duration
	dir   string
	mu    sync.RWMutex
	m     map[string]*minerState
}

func NewPoller(dir string, every time.Duration) *Poller {
	p := &Poller{Every: every, dir: dir, m: map[string]*minerState{}}
	go func() {
		for range time.Tick(time.Minute) {
			p.saveHistory()
		}
	}()
	return p
}

func srcFor(m Miner) source.Source {
	if m.Kind == "remote" {
		return &source.Remote{Base: m.RemoteURL, User: m.RemoteUser, Pass: m.RemotePass}
	}
	port := m.Port
	if port == 0 {
		port = 4028
	}
	return source.CGMiner{Addr: net.JoinHostPort(m.Host, strconv.Itoa(port))}
}

func (p *Poller) histFile(id string) string { return filepath.Join(p.dir, "history-"+id+".json") }

func (p *Poller) Start(m Miner) {
	st := &minerState{src: srcFor(m), stop: make(chan struct{})}
	if b, err := os.ReadFile(p.histFile(m.ID)); err == nil {
		_ = json.Unmarshal(b, &st.History)
	}
	p.mu.Lock()
	if old := p.m[m.ID]; old != nil {
		close(old.stop)
	}
	p.m[m.ID] = st
	p.mu.Unlock()
	go func() {
		p.pollOnce(m.ID, st)
		t := time.NewTicker(p.Every)
		defer t.Stop()
		for {
			select {
			case <-st.stop:
				return
			case <-t.C:
				p.pollOnce(m.ID, st)
			}
		}
	}()
}

func (p *Poller) Stop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st := p.m[id]; st != nil {
		close(st.stop)
		delete(p.m, id)
		os.Remove(p.histFile(id))
	}
}

func (p *Poller) pollOnce(id string, st *minerState) {
	snap := st.src.Fetch()
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, m := snap.Control, st.ledMemo; c != nil && m != nil && c.LEDHex == "" {
		c.LEDHex, c.LEDBright, c.LEDTemper = m.LEDHex, m.LEDBright, m.LEDTemper
	}
	st.Latest = snap
	if snap.OK {
		st.LastOK, st.LastErr = time.Now().Unix(), ""
		s := snap.Sample()
		if n := len(st.History); n == 0 || st.History[n-1].T != s.T {
			st.History = append(st.History, s)
		}
		if len(st.History) > historyMax {
			st.History = st.History[len(st.History)-historyMax:]
		}
	} else {
		st.LastErr = fmt.Sprint(snap.QueryErrors)
		log.Printf("miner %s: %s", id, st.LastErr)
	}
}

type LiveView struct {
	Latest  *model.Snapshot `json:"data"`
	LastOK  int64           `json:"last_ok"`
	LastErr string          `json:"last_err"`
	History []model.Sample  `json:"history"`
}

func (p *Poller) Live(id string, since int64) (LiveView, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := p.m[id]
	if st == nil {
		return LiveView{}, false
	}
	v := LiveView{Latest: st.Latest, LastOK: st.LastOK, LastErr: st.LastErr}
	for _, h := range st.History {
		if h.T > since {
			v.History = append(v.History, h)
		}
	}
	return v, true
}

func (p *Poller) saveHistory() {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, st := range p.m {
		b, _ := json.Marshal(st.History)
		_ = os.WriteFile(p.histFile(id), b, 0o600)
	}
}

var ErrNoControl = fmt.Errorf("this miner can't be controlled from here")

// Control runs one control action against a direct (CGMiner) miner, then re-reads it.
func (p *Poller) Control(id string, do func(source.Controller) (cgminer.Result, error), memo *model.Control) (cgminer.Result, *model.Snapshot, error) {
	p.mu.RLock()
	st := p.m[id]
	p.mu.RUnlock()
	if st == nil {
		return cgminer.Result{}, nil, fmt.Errorf("unknown miner")
	}
	ctl, ok := st.src.(source.Controller)
	if !ok {
		return cgminer.Result{}, nil, ErrNoControl
	}
	if !st.ctlMu.TryLock() {
		return cgminer.Result{}, nil, fmt.Errorf("another change is still being applied")
	}
	defer st.ctlMu.Unlock()
	if wait := 3*time.Second - time.Since(st.lastCtl); wait > 0 {
		return cgminer.Result{}, nil, fmt.Errorf("wait a few seconds between changes")
	}
	st.lastCtl = time.Now()
	res, err := do(ctl)
	if err == nil && res.OK && memo != nil {
		p.mu.Lock()
		st.ledMemo = memo
		p.mu.Unlock()
	}
	log.Printf("control %s: sent %q -> ok=%v status=%s msg=%q err=%v", id, res.Sent, res.OK, res.Status, res.Message, err)
	time.Sleep(1500 * time.Millisecond) // let the miner apply it before reading back
	p.pollOnce(id, st)
	p.mu.RLock()
	snap := st.Latest
	p.mu.RUnlock()
	return res, snap, err
}
