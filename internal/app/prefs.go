package app

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Prefs returns the persisted Allow phones on Wi-Fi and Gentle mode flags.
func (c *Config) Prefs() (wifi, gentle bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.AllowWifi != nil && *c.AllowWifi, c.Gentle
}

func (s *Server) prefsOut() map[string]any {
	wifi, gentle := s.Cfg.Prefs()
	var lan []string
	if !s.Locked && s.LANOK {
		for _, a := range LANAddrs() {
			lan = append(lan, "http://"+a+":"+itoa(s.Port))
		}
	}
	poll := DefaultPoll
	if s.Poll != nil {
		poll = s.Poll.Every
	}
	return map[string]any{
		"ok":           true,
		"allow_wifi":   wifi,
		"gentle":       gentle,
		"lan_urls":     lan,
		"poll_seconds": int(poll / time.Second),
	}
}

func (s *Server) prefsSet(w http.ResponseWriter, r *http.Request) {
	var b struct {
		AllowWifi *bool `json:"allow_wifi"`
		Gentle    *bool `json:"gentle"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if b.AllowWifi == nil && b.Gentle == nil {
		writeJSON(w, 400, map[string]string{"error": "nothing to change"})
		return
	}

	s.Cfg.mu.Lock()
	oldWifi := s.Cfg.AllowWifi != nil && *s.Cfg.AllowWifi
	if b.AllowWifi != nil {
		on := *b.AllowWifi
		s.Cfg.AllowWifi = &on
	}
	if b.Gentle != nil {
		s.Cfg.Gentle = *b.Gentle
	}
	wifi, gentle := s.Cfg.AllowWifi != nil && *s.Cfg.AllowWifi, s.Cfg.Gentle
	s.Cfg.mu.Unlock()

	if s.Poll != nil && b.Gentle != nil {
		s.Poll.SetGentle(gentle)
	}
	if err := s.Cfg.Save(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	out := s.prefsOut()
	if b.AllowWifi != nil && wifi != oldWifi {
		out["rebinding"] = true
		writeJSON(w, 200, out)
		go func() {
			time.Sleep(150 * time.Millisecond)
			if err := s.Rebind(ListenHost(wifi)); err != nil {
				log.Printf("rebind listener: %v", err)
			}
		}()
		return
	}
	writeJSON(w, 200, out)
}
