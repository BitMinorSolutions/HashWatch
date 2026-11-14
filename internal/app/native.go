package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hashwatch/hashwatch/internal/autostart"
	"github.com/hashwatch/hashwatch/internal/cgminer"
)

// Hooks used by the native app shells (macOS menu bar / Windows tray).

// ErrNeedPassword means remote access can't be switched on until a password exists.
var ErrNeedPassword = errors.New("set a password first")

// MenuStatus is what the menu bar / tray shows.
type MenuStatus struct {
	Hashrate  string // e.g. "3.85 TH/s" or "Offline"
	Detail    string // e.g. "Avalon Nano 3 · 92 °C"
	Remote    bool   // remote access switched on
	RemoteOK  bool   // the toggle is usable (not a preview)
	RemoteURL string
}

// Status sums up all miners for the menu.
func (s *Server) Status() MenuStatus {
	var ms MenuStatus
	var ths float64
	online, total := 0, 0
	var hottest float64
	name := ""
	for _, m := range s.Cfg.Miners {
		total++
		v, ok := s.Poll.Live(m.ID, 1<<62)
		if !ok || v.Latest == nil || !v.Latest.OK {
			continue
		}
		online++
		if name == "" {
			name = m.Name
		}
		h := v.Latest.Hashrate
		for _, p := range []*float64{h.THs5m, h.THs1m, h.THsHero} {
			if p != nil {
				ths += *p
				break
			}
		}
		if t := v.Latest.Temps.Max; t != nil && *t > hottest {
			hottest = *t
		}
	}
	switch {
	case total == 0:
		ms.Hashrate = "No miner yet"
	case online == 0:
		ms.Hashrate = "Miner offline"
	default:
		ms.Hashrate = fmt.Sprintf("%.2f TH/s", ths)
		if total > 1 {
			ms.Detail = fmt.Sprintf("%d of %d miners online", online, total)
		} else {
			ms.Detail = name
		}
		if hottest > 0 {
			ms.Detail += fmt.Sprintf(" · %.0f °C", hottest)
		}
	}
	ms.RemoteOK = s.Tunnel != nil && !s.Locked
	ms.Remote = s.Cfg.Remote.Enabled
	if s.Tunnel != nil {
		ms.RemoteURL = s.Tunnel.Status().URL
	}
	return ms
}

// SetRemote switches remote access from a native menu (the UI's password flow is
// needed the first time, so it returns ErrNeedPassword when none is set).
func (s *Server) SetRemote(on bool) error {
	if s.Tunnel == nil || s.Locked {
		return errors.New("remote access isn't available here")
	}
	remoteMu.Lock()
	defer remoteMu.Unlock()
	if on && s.Cfg.PasswordHash == "" {
		return ErrNeedPassword
	}
	s.Cfg.Remote.Enabled = on
	if on {
		s.Tunnel.Start()
	} else {
		s.Tunnel.Stop()
	}
	return s.Cfg.Save()
}

// debug returns a plain-text report for "Copy debug info" (no passwords, tokens or cookies).
func (s *Server) debug(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s/%s, %s)\nos: %s\n", AppName, s.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(), osVersion())
	fmt.Fprintf(&b, "time: %s\npreview: %v  port: %d  lan: %v  wifi: %v  gentle: %v  poll: %v\n", time.Now().Format(time.RFC3339), s.Locked, s.Port, s.LANOK, s.Cfg.WifiOn(), s.Cfg.Gentle, s.Poll.Every)
	if u := s.updateBrief(); true {
		fmt.Fprintf(&b, "update: service=%v url_set=%v channel=%v current=%v latest=%v\n", u["service"], u["url_set"], u["channel"], u["current"], u["latest"])
	}
	fmt.Fprintf(&b, "password set: %v  shell: %s\n", s.Cfg.PasswordHash != "", s.Shell)
	for _, m := range s.Cfg.Miners {
		v, _ := s.Poll.Live(m.ID, 1<<62)
		host := fmt.Sprintf("%s:%d", m.Host, m.Port)
		if m.Kind == "remote" {
			host = "remote preview"
		}
		fmt.Fprintf(&b, "miner %q (%s, %s): last_ok=%s poll=%ds", m.Name, m.Kind, host, unix(v.LastOK), v.Poll)
		if v.LastErr != "" {
			fmt.Fprintf(&b, " last_err=%q", v.LastErr)
		}
		if l := v.Latest; l != nil {
			fmt.Fprintf(&b, " ok=%v model=%s fw=%s", l.OK, l.Identity.Model, l.Identity.Firmware)
			if len(l.QueryErrors) > 0 {
				fmt.Fprintf(&b, " query_errors=%q", l.QueryErrors)
			}
		}
		b.WriteString("\n")
	}
	if s.Tunnel != nil {
		st := s.Tunnel.Status()
		fmt.Fprintf(&b, "remote: enabled=%v state=%s mode=%s restarts=%d cloudflared=%s", s.Cfg.Remote.Enabled, st.State, st.Mode, st.Restarts, st.Version)
		if st.Error != "" {
			fmt.Fprintf(&b, " error=%q", st.Error)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("remote: unavailable\n")
	}
	if s.Alerts != nil {
		items, _ := s.Alerts.Since(0, 5)
		for _, a := range items {
			fmt.Fprintf(&b, "alert %s %s: %s\n", unix(a.T), a.Kind, a.Title)
		}
	}
	fmt.Fprintf(&b, "autostart: %+v\n", autostart.Get(s.AutoStart))
	// Raw miner API replies (port 4028), so a problem can be diagnosed without the miner in hand.
	for _, m := range s.Cfg.Miners {
		if m.Kind == "remote" {
			continue
		}
		addr := net.JoinHostPort(m.Host, strconv.Itoa(m.Port))
		for _, cmd := range []string{"version", "summary", "stats", "estats", "pools"} {
			fmt.Fprintf(&b, "\n--- %s %s ---\n", addr, cmd)
			res, err := cgminer.Query(addr, cmd, 4*time.Second)
			if err != nil {
				fmt.Fprintf(&b, "error: %v\n", err)
				continue
			}
			js, _ := json.MarshalIndent(redact(res), "", " ")
			b.Write(js)
			b.WriteString("\n")
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(b.String()))
}

var secretKey = regexp.MustCompile(`(?i)pass|secret|token|key`)

// redact blanks password-like fields anywhere in a miner reply.
func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if secretKey.MatchString(k) {
				if str, ok := x.(string); !ok || str != "" {
					t[k] = "[redacted]"
				}
				continue
			}
			t[k] = redact(x)
		}
	case []any:
		for i := range t {
			t[i] = redact(t[i])
		}
	}
	return v
}

// osVersion is e.g. "macOS 15.1 (24B83)".
func osVersion() string {
	switch runtime.GOOS {
	case "darwin":
		v, _ := exec.Command("sw_vers", "-productVersion").Output()
		bld, _ := exec.Command("sw_vers", "-buildVersion").Output()
		return "macOS " + strings.TrimSpace(string(v)) + " (" + strings.TrimSpace(string(bld)) + ")"
	case "linux":
		if b, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(l, "PRETTY_NAME=") {
					return strings.Trim(strings.TrimPrefix(l, "PRETTY_NAME="), `"`)
				}
			}
		}
	}
	return runtime.GOOS
}

func unix(t int64) string {
	if t == 0 {
		return "never"
	}
	return time.Unix(t, 0).Format(time.RFC3339)
}
