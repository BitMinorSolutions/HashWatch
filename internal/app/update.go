package app

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashwatch/hashwatch/internal/update"
)

// ServiceMode is set when HashWatch is the always-on systemd service (the Pi).
func ServiceMode() bool { return os.Getenv("HASHWATCH_SERVICE") == "1" }

func (c *Config) updateURL() string {
	if v := strings.TrimSpace(os.Getenv("HASHWATCH_UPDATE_URL")); v != "" {
		return update.ManifestURL(v)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if u := strings.TrimSpace(c.UpdateURL); u != "" {
		return update.ManifestURL(u)
	}
	return update.ManifestURL(update.DefaultReleaseURL)
}

func (c *Config) updateChannel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return update.NormalizeChannel(c.UpdateChannel)
}

func (c *Config) setUpdateChannel(ch string) {
	c.mu.Lock()
	c.UpdateChannel = update.NormalizeChannel(ch)
	c.mu.Unlock()
}

func (c *Config) setUpdateURL(u string) {
	c.mu.Lock()
	c.UpdateURL = strings.TrimSpace(u)
	c.mu.Unlock()
}

// NewUpdateRunner builds the OTA runner for this process. The runner never
// talks to a miner; it only fetches the configured manifest URL.
func NewUpdateRunner(cfg *Config, version, exe, dir string) *update.Runner {
	return &update.Runner{
		Version:  version,
		Exe:      exe,
		Dir:      dir,
		Service:  ServiceMode(),
		Interval: update.Interval,
		URL:      cfg.updateURL,
		Channel:  cfg.updateChannel,
	}
}

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	if s.Update == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "current": s.Version, "service": ServiceMode()})
		return
	}
	writeJSON(w, 200, s.Update.Snapshot())
}

func (s *Server) updateSet(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Channel *string `json:"channel"`
		URL     *string `json:"update_url"`
		Check   bool    `json:"check"`
		Apply   bool    `json:"apply"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if b.Channel == nil && b.URL == nil && !b.Check && !b.Apply {
		writeJSON(w, 400, map[string]string{"error": "nothing to change"})
		return
	}
	if s.Update == nil {
		writeJSON(w, 400, map[string]string{"error": "updates aren't available in this copy"})
		return
	}
	changed := false
	if b.Channel != nil {
		ch := update.NormalizeChannel(*b.Channel)
		if ch != "stable" && ch != "dev" {
			writeJSON(w, 400, map[string]string{"error": "channel must be stable or dev"})
			return
		}
		s.Cfg.setUpdateChannel(ch)
		changed = true
	}
	if b.URL != nil {
		u := strings.TrimSpace(*b.URL)
		if u != "" && !strings.HasPrefix(strings.ToLower(u), "https://") && !strings.HasPrefix(strings.ToLower(u), "http://") {
			writeJSON(w, 400, map[string]string{"error": "update URL must be http(s)"})
			return
		}
		s.Cfg.setUpdateURL(u)
		changed = true
	}
	if changed {
		if err := s.Cfg.Save(); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	var st update.Status
	var err error
	switch {
	case b.Apply:
		st, err = s.Update.ApplyNow(ctx)
	case b.Check || changed:
		st, err = s.Update.Check(ctx, false)
	default:
		st = s.Update.Snapshot()
	}
	out := map[string]any{"ok": err == nil, "update": st}
	if err != nil {
		out["error"] = err.Error()
		// Still 200 when we only failed to reach the host after saving a channel —
		// but apply/check failures should be reported as 400/502.
		writeJSON(w, 502, out)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) updateBrief() map[string]any {
	if s.Update == nil {
		return map[string]any{"enabled": false, "current": s.Version, "service": ServiceMode()}
	}
	st := s.Update.Snapshot()
	return map[string]any{
		"enabled":     st.Enabled,
		"service":     st.Service,
		"channel":     st.Channel,
		"current":     st.Current,
		"latest":      st.Latest,
		"available":   st.Available,
		"can_apply":   st.CanApply,
		"error":       st.Error,
		"url_set":     st.URLSet,
		"has_key":     st.HasKey,
		"rolled_back": st.RolledBack,
		"notes":       st.Notes,
		"last_check":  st.LastCheck,
	}
}

// ConfirmPendingUpdate waits until this copy answers /api/ping, then marks a
// pending OTA as healthy — or rolls the binary back if the health check fails.
func (s *Server) ConfirmPendingUpdate() {
	if s == nil || s.Update == nil || s.Update.Dir == "" {
		return
	}
	if _, err := update.ReadPending(s.Update.Dir); err != nil {
		return
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s.pingOK() {
			rolled, err := update.Confirm(s.Update.Dir, s.Update.Exe, s.Version, true)
			if err != nil {
				log.Printf("update: confirm: %v", err)
				return
			}
			if rolled {
				log.Printf("update: new version failed its health check; rolled back")
				if err := s.restartAfterRollback(); err != nil {
					log.Printf("update: restart after rollback: %v", err)
				}
			} else {
				log.Printf("update: %s is healthy", s.Version)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("update: health check did not succeed in time; supervise will roll back if needed")
}

func (s *Server) pingOK() bool {
	if s.Port <= 0 {
		return false
	}
	c := http.Client{Timeout: 1500 * time.Millisecond}
	r, err := c.Get(LocalURL(s.Port) + "/api/ping")
	if err != nil {
		return false
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return false
	}
	var p struct {
		AppID   string `json:"app_id"`
		Version string `json:"version"`
	}
	if json.NewDecoder(r.Body).Decode(&p) != nil {
		return false
	}
	return p.AppID == AppID && (p.Version == "" || p.Version == s.Version)
}

func (s *Server) restartAfterRollback() error {
	if s.Update == nil {
		return nil
	}
	return s.Update.RestartNow()
}
