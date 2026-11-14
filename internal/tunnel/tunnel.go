// Package tunnel runs Cloudflare's cloudflared to give Hashwatch a public
// https link ("Remote access"). The official binary is downloaded on first use
// from Cloudflare's GitHub releases, checked against the SHA-256 published for
// that release, and cached in the app's config folder.
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Status struct {
	State    string  `json:"state"` // off, downloading, starting, online, restarting, error
	URL      string  `json:"url"`
	Progress float64 `json:"progress"` // 0..1 while downloading
	Detail   string  `json:"detail"`
	Error    string  `json:"error"`
	Since    int64   `json:"since"`
	Restarts int     `json:"restarts"`
	Mode     string  `json:"mode"` // quick or named
	Version  string  `json:"cloudflared"`
}

type Manager struct {
	Dir      string // cache folder (…/Hashwatch/cloudflared)
	Origin   string // http://127.0.0.1:PORT
	Token    string // optional: a named tunnel token for a permanent hostname
	Hostname string // the hostname that token routes to
	OnURL    func(url string)

	mu     sync.Mutex
	st     Status
	cancel context.CancelFunc
	done   chan struct{}
}

var quickURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	if s.State == "" {
		s.State = "off"
	}
	return s
}

func (m *Manager) set(f func(*Status)) {
	m.mu.Lock()
	f(&m.st)
	m.mu.Unlock()
}

// Running reports whether Start was called and Stop has not been.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

// Start downloads cloudflared if needed and keeps a tunnel up until Stop.
func (m *Manager) Start() {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.done = cancel, make(chan struct{})
	mode := "quick"
	if m.Token != "" {
		mode = "named"
	}
	m.st = Status{State: "starting", Mode: mode, Since: time.Now().Unix()}
	done := m.done
	m.mu.Unlock()
	go func() {
		defer close(done)
		m.loop(ctx)
	}()
}

// Stop ends the tunnel and waits for cloudflared to exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	m.set(func(s *Status) { *s = Status{State: "off"} })
}

func (m *Manager) loop(ctx context.Context) {
	bin, err := m.ensureBinary(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.set(func(s *Status) { s.State, s.Error = "error", err.Error() })
			log.Printf("remote access: %v", err)
		}
		return
	}
	if out, err := exec.Command(bin, "--version").Output(); err == nil {
		v := strings.Fields(string(out))
		if len(v) >= 3 {
			m.set(func(s *Status) { s.Version = v[2] })
		}
	}
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := m.runOnce(ctx, bin)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > 10*time.Minute {
			backoff = 2 * time.Second
		}
		log.Printf("remote access: cloudflared stopped (%v); restarting in %s", err, backoff)
		m.set(func(s *Status) {
			s.State, s.Restarts, s.Detail = "restarting", s.Restarts+1, fmt.Sprintf("Reconnecting in %ds…", int(backoff/time.Second))
			if err != nil {
				s.Error = err.Error()
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// runOnce starts cloudflared and watches it: the process exiting, or its
// /ready health endpoint failing for two minutes, ends the run.
func (m *Manager) runOnce(ctx context.Context, bin string) error {
	mp := freePort()
	args := []string{"tunnel", "--no-autoupdate", "--metrics", "127.0.0.1:" + strconv.Itoa(mp)}
	if m.Token != "" {
		args = append(args, "run")
	} else {
		args = append(args, "--url", m.Origin)
	}
	rctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(rctx, bin, args...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error { return stopProc(cmd) }
	cmd.Env = os.Environ()
	if m.Token != "" {
		cmd.Env = append(cmd.Env, "TUNNEL_TOKEN="+m.Token) // keeps the token out of the process list
	}
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	m.set(func(s *Status) { s.State, s.Detail, s.URL = "starting", "Starting cloudflared…", "" })
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			// cloudflared prints the quick-tunnel link as soon as Cloudflare
			// reserves it; it only works once a connection is registered.
			if u := quickURL.FindString(line); u != "" && m.Token == "" {
				m.set(func(s *Status) { s.URL, s.Detail = u, "Link reserved · connecting to Cloudflare…" })
			}
			if strings.Contains(line, "Registered tunnel connection") && m.Status().State != "online" {
				u := m.Status().URL
				if m.Token != "" {
					u = "https://" + strings.TrimPrefix(strings.TrimPrefix(m.Hostname, "https://"), "http://")
				}
				if u != "" {
					m.online(u)
				}
			}
			if strings.Contains(line, " ERR ") {
				m.set(func(s *Status) { s.Detail = trim(line) })
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); pw.Close() }()
	fails, begun := 0, time.Now()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			return err
		case <-tick.C:
			if m.Status().State != "online" {
				if time.Since(begun) > 4*time.Minute { // never connected: try a fresh start
					stop()
					<-exited
					return errors.New("couldn't connect to Cloudflare. Check that this computer can reach the internet (outbound port 7844)")
				}
				continue
			}
			c := http.Client{Timeout: 5 * time.Second}
			r, err := c.Get("http://127.0.0.1:" + strconv.Itoa(mp) + "/ready")
			if err == nil {
				r.Body.Close()
			}
			if err != nil || r.StatusCode != 200 {
				fails++
				m.set(func(s *Status) { s.Detail = "Cloudflare connection lost; waiting for it to come back…" })
			} else {
				if fails > 0 {
					m.set(func(s *Status) { s.Detail = "" })
				}
				fails = 0
			}
			if fails >= 4 {
				stop()
				<-exited
				return errors.New("tunnel was not ready for 2 minutes")
			}
		}
	}
}

func trim(s string) string {
	if i := strings.Index(s, " ERR "); i >= 0 {
		s = s[i+5:]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func (m *Manager) online(url string) {
	m.set(func(s *Status) {
		s.State, s.URL, s.Detail, s.Error, s.Since = "online", url, "", "", time.Now().Unix()
	})
	if m.OnURL != nil {
		m.OnURL(url)
	}
}
