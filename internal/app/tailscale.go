package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/qr"
)

// TailscaleInfo is a detected Tailscale address for this computer. HashWatch
// never installs Tailscale; it only reads what's already there.
type TailscaleInfo struct {
	Available bool   `json:"available"`
	IP        string `json:"ip,omitempty"`
	Name      string `json:"name,omitempty"` // MagicDNS, no trailing dot
	HTTP      string `json:"http,omitempty"`
	HTTPS     string `json:"https,omitempty"`
	Serve     bool   `json:"serve,omitempty"`
	URL       string `json:"url,omitempty"` // best link to open: https if serve is on, else http
	Error     string `json:"error,omitempty"`
}

var (
	tsMu    sync.Mutex
	tsCache TailscaleInfo
	tsAt    time.Time
	tsTTL   = 30 * time.Second
)

func tailscaleBin() string {
	if v := os.Getenv("HASHWATCH_TAILSCALE"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
		return ""
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	return ""
}

func runTailscale(ctx context.Context, args ...string) (string, error) {
	bin := tailscaleBin()
	if bin == "" {
		return "", exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// LookupTailscale detects the Tailscale IPv4 address, MagicDNS name and, when
// `tailscale serve` is already proxying this dashboard, the HTTPS URL.
func LookupTailscale(port int) TailscaleInfo {
	tsMu.Lock()
	defer tsMu.Unlock()
	if time.Since(tsAt) < tsTTL && tsCache.Available {
		return withPort(tsCache, port)
	}
	info := probeTailscale()
	tsCache, tsAt = info, time.Now()
	return withPort(info, port)
}

func withPort(in TailscaleInfo, port int) TailscaleInfo {
	if !in.Available || port <= 0 {
		return in
	}
	host := in.Name
	if host == "" {
		host = in.IP
	}
	if host != "" && in.HTTP == "" {
		in.HTTP = "http://" + host + ":" + itoa(port)
	}
	if in.HTTPS == "" && in.Name != "" && in.Serve {
		in.HTTPS = "https://" + in.Name
	}
	in.URL = in.HTTPS
	if in.URL == "" {
		in.URL = in.HTTP
	}
	return in
}

func probeTailscale() TailscaleInfo {
	var info TailscaleInfo
	if tailscaleBin() == "" {
		return info
	}
	info.Available = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if ip, err := runTailscale(ctx, "ip", "-4"); err == nil {
		for _, line := range strings.Split(ip, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.Contains(line, " ") {
				info.IP = line
				break
			}
		}
	}
	if raw, err := runTailscale(ctx, "status", "--json"); err == nil {
		var st struct {
			Self struct {
				DNSName string `json:"DNSName"`
				Online  bool   `json:"Online"`
			} `json:"Self"`
		}
		if json.Unmarshal([]byte(raw), &st) == nil {
			info.Name = strings.TrimSuffix(st.Self.DNSName, ".")
		}
	}
	if raw, err := runTailscale(ctx, "serve", "status", "--json"); err == nil && raw != "" && raw != "{}" {
		if u := serveHTTPS(raw, info.Name); u != "" {
			info.HTTPS, info.Serve = u, true
		} else if !strings.Contains(strings.ToLower(raw), "no serve") {
			// serve is configured in some form we didn't parse
			if info.Name != "" {
				info.HTTPS, info.Serve = "https://"+info.Name, true
			}
		}
	}
	if info.IP == "" && info.Name == "" {
		info.Error = "Tailscale is installed, but this computer doesn't have an address yet. Sign in with: tailscale up"
	}
	return info
}

func serveHTTPS(raw, name string) string {
	// Accept a few shapes of `tailscale serve status --json` from recent clients.
	if name != "" && strings.Contains(raw, "https://"+name) {
		return "https://" + name
	}
	var generic map[string]any
	if json.Unmarshal([]byte(raw), &generic) != nil {
		return ""
	}
	if web, ok := generic["Web"].(map[string]any); ok {
		for host := range web {
			h := strings.TrimSuffix(host, ".")
			if strings.Contains(h, ":443") || strings.HasPrefix(h, "https://") {
				h = strings.TrimPrefix(h, "https://")
				h = strings.TrimSuffix(h, ":443")
				if h != "" {
					return "https://" + h
				}
			}
		}
	}
	return ""
}

func (s *Server) tailscaleBrief() map[string]any {
	info := LookupTailscale(s.Port)
	b, _ := json.Marshal(info)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Server) tailscaleGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, LookupTailscale(s.Port))
}

func (s *Server) tailscaleQR(w http.ResponseWriter, r *http.Request) {
	info := LookupTailscale(s.Port)
	u := r.URL.Query().Get("u")
	if u == "" || (u != info.URL && u != info.HTTP && u != info.HTTPS) {
		http.NotFound(w, r)
		return
	}
	c, err := qr.Encode([]byte(u))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(c.SVG("#0b0c0e", "#ffffff")))
}

func (s *Server) tailscaleServe(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if !IsLocal(r) {
		writeJSON(w, 403, map[string]string{"error": "For safety, this can only be changed from your home network."})
		return
	}
	if tailscaleBin() == "" {
		writeJSON(w, 400, map[string]string{"error": "Tailscale isn't installed on this computer."})
		return
	}
	if s.Port <= 0 {
		writeJSON(w, 409, map[string]string{"error": "The dashboard isn't listening yet."})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var out string
	var err error
	if b.Enabled {
		out, err = runTailscale(ctx, "serve", "--bg", "http://127.0.0.1:"+itoa(s.Port))
	} else {
		out, err = runTailscale(ctx, "serve", "reset")
	}
	tsMu.Lock()
	tsAt = time.Time{} // refresh on next lookup
	tsMu.Unlock()
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		writeJSON(w, 502, map[string]string{"error": "tailscale serve: " + msg})
		return
	}
	writeJSON(w, 200, LookupTailscale(s.Port))
}

func resetTailscaleCache() {
	tsMu.Lock()
	tsCache, tsAt = TailscaleInfo{}, time.Time{}
	tsMu.Unlock()
}
