package app

import (
	"encoding/json"
	"net/http"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/source"
)

func (s *Server) advancedOK() bool {
	s.Cfg.mu.Lock()
	defer s.Cfg.mu.Unlock()
	return s.Cfg.Advanced
}

// requireAdminPassword re-checks the HashWatch admin password (never the
// view-only password). Advanced writes always need this, even with a live session.
func (s *Server) requireAdminPassword(w http.ResponseWriter, pw string) bool {
	if s.Cfg.PasswordHash == "" {
		writeJSON(w, 403, map[string]string{"error": "Set an admin password in Settings first. Advanced miner controls need it."})
		return false
	}
	if !s.Cfg.CheckPassword(pw) {
		writeJSON(w, 403, map[string]string{"error": "That isn't the admin password."})
		return false
	}
	return true
}

func (s *Server) requireAdvanced(w http.ResponseWriter, id string) bool {
	if !s.advancedOK() {
		writeJSON(w, 403, map[string]string{"error": "Advanced miner controls are off. Turn them on in Settings first."})
		return false
	}
	if !s.canControl(id) {
		writeJSON(w, 403, map[string]string{"error": ErrNoControl.Error()})
		return false
	}
	return true
}

func (s *Server) ctlPool(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Miner      string `json:"miner"`
		URL        string `json:"url"`
		Worker     string `json:"worker"`
		WorkerPass string `json:"worker_pass"`
		WebUser    string `json:"web_user"`
		WebPass    string `json:"web_pass"`
		Password   string `json:"password"`
		Pool       int    `json:"pool"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if !s.requireAdminPassword(w, b.Password) || !s.requireAdvanced(w, b.Miner) {
		return
	}
	if _, err := cgminer.SetPoolParam(b.WebUser, b.WebPass, b.Pool, b.URL, b.Worker, b.WorkerPass); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	res, snap, err := s.Poll.Control(b.Miner, func(c source.Controller) (cgminer.Result, error) {
		return c.SetPool(b.WebUser, b.WebPass, b.Pool, b.URL, b.Worker, b.WorkerPass)
	}, nil)
	s.ctlReply(w, res, snap, err)
}

func (s *Server) ctlReboot(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Miner    string `json:"miner"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if !s.requireAdminPassword(w, b.Password) || !s.requireAdvanced(w, b.Miner) {
		return
	}
	// Don't wait to read the miner back: a real reboot drops the API.
	res, err := s.Poll.Command(b.Miner, func(c source.Controller) (cgminer.Result, error) {
		return c.Reboot()
	})
	s.ctlReply(w, res, s.Poll.Latest(b.Miner), err)
}
