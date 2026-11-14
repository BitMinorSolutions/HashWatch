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
}

func instPath(dir string) string { return filepath.Join(dir, "instance.json") }

// WriteInstance records this running copy so a second launch can find it.
func WriteInstance(dir string, port int, id, url string) {
	b, _ := json.Marshal(instanceFile{os.Getpid(), port, id, url})
	_ = os.WriteFile(instPath(dir), b, 0o600)
}

func RemoveInstance(dir, id string) {
	var f instanceFile
	if b, err := os.ReadFile(instPath(dir)); err == nil && json.Unmarshal(b, &f) == nil && f.ID == id {
		os.Remove(instPath(dir))
	}
}

// RunningInstance returns the URL of a copy already running with this
// settings folder, verified by asking it (/api/ping) for its instance id.
func RunningInstance(dir string) (string, bool) {
	b, err := os.ReadFile(instPath(dir))
	if err != nil {
		return "", false
	}
	var f instanceFile
	if json.Unmarshal(b, &f) != nil || f.Port == 0 || f.ID == "" {
		return "", false
	}
	c := http.Client{Timeout: 1500 * time.Millisecond}
	r, err := c.Get("http://127.0.0.1:" + strconv.Itoa(f.Port) + "/api/ping")
	if err != nil {
		return "", false
	}
	defer r.Body.Close()
	var p struct{ App, ID string }
	if json.NewDecoder(r.Body).Decode(&p) != nil || p.App != AppName || p.ID != f.ID {
		return "", false
	}
	return f.URL, true
}
