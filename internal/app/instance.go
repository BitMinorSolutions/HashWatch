package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type instanceFile struct {
	PID  int    `json:"pid"`
	Port int    `json:"port"`
	ID   string `json:"id"`
	URL  string `json:"url"`
	App  string `json:"app_id,omitempty"`
}

func instPath(dir string) string { return filepath.Join(dir, "instance.json") }

// LocalURL is the only address the window, menu bar and hand-off ever load for this copy.
func LocalURL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

// WriteInstance records this running copy so a second launch can find it.
func WriteInstance(dir string, port int, id string) {
	b, _ := json.Marshal(instanceFile{os.Getpid(), port, id, LocalURL(port), AppID})
	_ = os.WriteFile(instPath(dir), b, 0o600)
}

func RemoveInstance(dir, id string) {
	var f instanceFile
	if b, err := os.ReadFile(instPath(dir)); err == nil && json.Unmarshal(b, &f) == nil && f.ID == id {
		os.Remove(instPath(dir))
	}
}

// RunningInstance returns the URL of a copy already running with this settings folder.
// It hands off only to a server that proves it is that copy: /api/ping must answer with
// HashWatch's app id, this build's name and the random instance id (and pid) recorded in
// instance.json. Any other program on that port (or a stale file) means "not running".
func RunningInstance(dir string) (string, bool) {
	b, err := os.ReadFile(instPath(dir))
	if err != nil {
		return "", false
	}
	var f instanceFile
	if json.Unmarshal(b, &f) != nil || f.Port <= 0 || f.Port > 65535 || len(f.ID) < 8 {
		return "", false
	}
	c := http.Client{Timeout: 1500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Get(LocalURL(f.Port) + "/api/ping")
	if err != nil {
		return "", false
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", false
	}
	var p struct {
		App   string `json:"app"`
		AppID string `json:"app_id"`
		ID    string `json:"id"`
		PID   int    `json:"pid"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096)).Decode(&p) != nil ||
		p.AppID != AppID || p.App != AppName || p.ID != f.ID || (f.PID != 0 && p.PID != f.PID) {
		return "", false
	}
	return LocalURL(f.Port), true
}
