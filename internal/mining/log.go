package mining

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/model"
)

// Keep is how long accepted-share hits are stored (same 24h window as the charts).
const Keep = 24 * 3600

const maxHits = 20000

// Log is the per-miner rolling share-hit history and block-height clock,
// persisted next to the other sparkline files.
type Log struct {
	dir string
	mu  sync.Mutex
	st  map[string]*state
}

type state struct {
	prev      Sample
	havePrev  bool
	Hits      []Hit       `json:"hits"`
	Height    HeightState `json:"height"`
	BestShare float64     `json:"best_share,omitempty"`
	WorkDiff  float64     `json:"work_diff,omitempty"`
}

// View is the Mining data included on /api/live.
type View struct {
	Hits      []Hit       `json:"hits"`
	Height    HeightState `json:"height"`
	BestShare float64     `json:"best_share,omitempty"`
	WorkDiff  float64     `json:"work_diff,omitempty"`
	LastHit   *Hit        `json:"last_hit,omitempty"`
}

func NewLog(dir string) *Log { return &Log{dir: dir, st: map[string]*state{}} }

func (l *Log) file(id string) string { return filepath.Join(l.dir, "mining-"+id+".json") }

func (l *Log) load(id string) *state {
	if l.st == nil {
		l.st = map[string]*state{}
	}
	if s := l.st[id]; s != nil {
		return s
	}
	s := &state{}
	if l.dir != "" {
		if b, err := os.ReadFile(l.file(id)); err == nil {
			_ = json.Unmarshal(b, s)
		}
	}
	l.st[id] = s
	return s
}

// Observe records a poll: maybe one share hit, maybe a height change.
func (l *Log) Observe(id string, snap *model.Snapshot, now int64) *Hit {
	if l == nil {
		return nil
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	cur := FromSnapshot(snap)
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load(id)
	var prev *Sample
	if s.havePrev {
		p := s.prev
		prev = &p
	}
	var hit *Hit
	if cur.OK {
		hit = DetectShare(prev, cur, now)
		if hit != nil {
			s.Hits = append(s.Hits, *hit)
			s.Hits = trimHits(s.Hits, now)
		}
		hs := DetectHeight(prev, cur, s.Height.ChangedAt, s.Height.Height, now)
		s.Height = hs
		if cur.BestShare > s.BestShare {
			s.BestShare = cur.BestShare
		}
		if cur.HasWorkDiff {
			s.WorkDiff = cur.WorkDiff
		}
		s.prev, s.havePrev = cur, true
	}
	return hit
}

func trimHits(h []Hit, now int64) []Hit {
	cut := now - Keep
	i := 0
	for i < len(h) && h[i].T < cut {
		i++
	}
	if i > 0 {
		h = append([]Hit(nil), h[i:]...)
	}
	if len(h) > maxHits {
		h = append([]Hit(nil), h[len(h)-maxHits:]...)
	}
	return h
}

// ViewSince returns hits at or after since (0 = all retained).
func (l *Log) View(id string, since int64) View {
	if l == nil {
		return View{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.load(id)
	v := View{Height: s.Height, BestShare: s.BestShare, WorkDiff: s.WorkDiff}
	for i := range s.Hits {
		if since > 0 && s.Hits[i].T < since {
			continue
		}
		v.Hits = append(v.Hits, s.Hits[i])
	}
	if n := len(s.Hits); n > 0 {
		h := s.Hits[n-1]
		v.LastHit = &h
	}
	return v
}

func (l *Log) Save() {
	if l == nil || l.dir == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, s := range l.st {
		b, err := json.Marshal(s)
		if err != nil {
			continue
		}
		_ = os.WriteFile(l.file(id), b, 0o600)
	}
}

func (l *Log) Remove(id string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.st, id)
	if l.dir != "" {
		_ = os.Remove(l.file(id))
	}
}
