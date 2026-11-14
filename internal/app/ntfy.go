package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/qr"
)

// DefaultNtfyServer is the public ntfy instance. Users can point at their own.
const DefaultNtfyServer = "https://ntfy.sh"

// NtfySettings is optional iPhone / Apple Watch delivery via the ntfy app.
// The access token is never written into a notification body or subscribe URL.
type NtfySettings struct {
	Enabled bool            `json:"enabled,omitempty"`
	Server  string          `json:"server,omitempty"`
	Topic   string          `json:"topic,omitempty"`
	Token   string          `json:"token,omitempty"`
	Off     map[string]bool `json:"off,omitempty"` // per-kind; missing means on
}

// NtfyKinds are the alerts that can be forwarded to ntfy.
var NtfyKinds = []AlertKind{
	{"block", "Block found", "Urgent. A new Found Blocks count on the miner."},
	{"offline", "Miner offline", "High priority when a miner drops off, and when it returns."},
	{"hashrate", "Hashrate drop", "When hashrate stays well below its recent average."},
	{"temp", "Overheat", "High priority when a miner runs hot."},
	{"summary", "Daily summary", "The once-a-day briefing, if that alert is on."},
}

func ntfyPriority(kind string) string {
	switch kind {
	case "block":
		return "5" // urgent / max
	case "offline", "temp":
		return "4" // high
	default:
		return "3"
	}
}

func newNtfyTopic() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// extremely unlikely; still return something unique-ish
		return "hw" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "hw" + hex.EncodeToString(b[:])
}

func normalizeNtfyServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultNtfyServer, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("The ntfy server must be an http or https URL.")
	}
	if u.User != nil {
		return "", fmt.Errorf("Don't put a username or password in the ntfy server URL. Use the access token field.")
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

func validNtfyTopic(t string) bool {
	if len(t) < 16 || len(t) > 64 {
		return false
	}
	for _, r := range t {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func (n NtfySettings) server() string {
	if strings.TrimSpace(n.Server) == "" {
		return DefaultNtfyServer
	}
	return strings.TrimRight(strings.TrimSpace(n.Server), "/")
}

func (n NtfySettings) SubscribeURL() string {
	if n.Topic == "" {
		return ""
	}
	return n.server() + "/" + n.Topic
}

func (n NtfySettings) AppLink() string {
	u := n.SubscribeURL()
	if u == "" {
		return ""
	}
	return strings.Replace(u, "https://", "ntfy://", 1)
}

func (n NtfySettings) kindOn(kind string) bool {
	if kind == "test" {
		return true
	}
	if n.Off[kind] {
		return false
	}
	for _, k := range NtfyKinds {
		if k.Kind == kind {
			return true
		}
	}
	return false
}

func (c *Config) ensureNtfyTopic() string {
	c.mu.Lock()
	if c.Ntfy.Topic == "" || !validNtfyTopic(c.Ntfy.Topic) {
		c.Ntfy.Topic = newNtfyTopic()
		c.mu.Unlock()
		_ = c.Save()
		return c.Ntfy.Topic
	}
	t := c.Ntfy.Topic
	c.mu.Unlock()
	return t
}

func (c *Config) ntfyCopy() NtfySettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.Ntfy
	if out.Off != nil {
		m := make(map[string]bool, len(out.Off))
		for k, v := range out.Off {
			m[k] = v
		}
		out.Off = m
	}
	return out
}

func scrubNtfy(s, token string) string {
	if token != "" && s != "" && strings.Contains(s, token) {
		s = strings.ReplaceAll(s, token, "…")
	}
	// never leak typical secret field names if they were ever interpolated
	return s
}

// ntfySender posts alerts to an ntfy server with dedupe and rate limits.
type ntfySender struct {
	client *http.Client
	mu     sync.Mutex
	last   map[string]time.Time
	sent   []time.Time
	now    func() time.Time
}

var defaultNtfy = &ntfySender{
	client: &http.Client{Timeout: 8 * time.Second},
	last:   map[string]time.Time{},
	now:    time.Now,
}

func (s *ntfySender) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

const (
	ntfyDedupe     = 15 * time.Minute
	ntfyTestDedupe = 45 * time.Second
	ntfyKindWindow = 10 * time.Minute
	ntfyKindMax    = 2
	ntfyHourMax    = 24
)

func ntfyKey(al Alert) string {
	return al.Kind + "|" + al.Miner + "|" + al.Title + "|" + al.Body
}

// allow reports whether this alert may be posted. The caller must hold s.mu.
func (s *ntfySender) allow(al Alert) (ok bool, reason string) {
	now := s.clock()
	key := ntfyKey(al)
	win := ntfyDedupe
	if al.Kind == "test" {
		win = ntfyTestDedupe
	}
	if t, ok := s.last[key]; ok && now.Sub(t) < win {
		return false, "duplicate"
	}
	if al.Kind != "block" && al.Kind != "test" {
		n := 0
		for k, t := range s.last {
			if strings.HasPrefix(k, al.Kind+"|"+al.Miner+"|") && now.Sub(t) < ntfyKindWindow {
				n++
			}
		}
		if n >= ntfyKindMax {
			return false, "rate limit"
		}
	}
	if al.Kind != "block" {
		hour := 0
		cut := now.Add(-time.Hour)
		for _, t := range s.sent {
			if t.After(cut) {
				hour++
			}
		}
		if hour >= ntfyHourMax {
			return false, "rate limit"
		}
	}
	return true, ""
}

func (s *ntfySender) remember(al Alert) {
	now := s.clock()
	if s.last == nil {
		s.last = map[string]time.Time{}
	}
	s.last[ntfyKey(al)] = now
	s.sent = append(s.sent, now)
	cut := now.Add(-2 * time.Hour)
	out := s.sent[:0]
	for _, t := range s.sent {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	s.sent = out
}

// Send posts one alert. ok is false when the kind is off, ntfy is disabled,
// or the limiter drops it. err is only a delivery failure.
func (s *ntfySender) Send(cfg NtfySettings, al Alert) (ok bool, err error) {
	if !cfg.Enabled || cfg.Topic == "" || !cfg.kindOn(al.Kind) {
		return false, nil
	}
	s.mu.Lock()
	allow, _ := s.allow(al)
	if !allow {
		s.mu.Unlock()
		return false, nil
	}
	s.mu.Unlock()

	title := scrubNtfy(al.Title, cfg.Token)
	body := scrubNtfy(al.Body, cfg.Token)
	if al.MinerName != "" && al.Kind == "block" && !strings.Contains(title, al.MinerName) {
		title = title + " · " + al.MinerName
	}
	if title == "" {
		title = AppName
	}
	if body == "" {
		body = "Open " + AppName + " for details."
	}

	u := cfg.server() + "/" + cfg.Topic
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", ntfyPriority(al.Kind))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	// Never put the token, password, or server credentials in the body or title.
	cli := s.client
	if cli == nil {
		cli = defaultNtfy.client
	}
	res, err := cli.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	io.CopyN(io.Discard, res.Body, 4096)
	if res.StatusCode >= 300 {
		return false, fmt.Errorf("ntfy returned %s", res.Status)
	}
	s.mu.Lock()
	s.remember(al)
	s.mu.Unlock()
	return true, nil
}

func (s *Server) ntfyView() map[string]any {
	s.Cfg.ensureNtfyTopic()
	n := s.Cfg.ntfyCopy()
	if n.Server == "" {
		n.Server = DefaultNtfyServer
	}
	var kinds []map[string]any
	for _, k := range NtfyKinds {
		kinds = append(kinds, map[string]any{"kind": k.Kind, "label": k.Label, "desc": k.Desc, "on": n.kindOn(k.Kind)})
	}
	return map[string]any{
		"enabled":       n.Enabled,
		"server":        n.Server,
		"topic":         n.Topic,
		"token_set":     n.Token != "",
		"subscribe_url": n.SubscribeURL(),
		"app_link":      n.AppLink(),
		"kinds":         kinds,
	}
}

func (s *Server) ntfyGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.ntfyView())
}

func (s *Server) ntfyQR(w http.ResponseWriter, r *http.Request) {
	n := s.Cfg.ntfyCopy()
	if n.Topic == "" {
		s.Cfg.ensureNtfyTopic()
		n = s.Cfg.ntfyCopy()
	}
	u := r.URL.Query().Get("u")
	if u == "" || (u != n.SubscribeURL() && u != n.AppLink()) {
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

func (s *Server) ntfySet(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled    *bool           `json:"enabled"`
		Server     *string         `json:"server"`
		Topic      *string         `json:"topic"`
		Token      *string         `json:"token"`
		ClearToken bool            `json:"clear_token"`
		NewTopic   bool            `json:"new_topic"`
		Kinds      map[string]bool `json:"kinds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	s.Cfg.mu.Lock()
	if b.Server != nil {
		u, err := normalizeNtfyServer(*b.Server)
		if err != nil {
			s.Cfg.mu.Unlock()
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.Cfg.Ntfy.Server = u
	}
	if b.NewTopic {
		s.Cfg.Ntfy.Topic = newNtfyTopic()
	} else if b.Topic != nil {
		t := strings.TrimSpace(*b.Topic)
		if !validNtfyTopic(t) {
			s.Cfg.mu.Unlock()
			writeJSON(w, 400, map[string]string{"error": "The topic must be 16–64 letters, numbers, dashes or underscores."})
			return
		}
		s.Cfg.Ntfy.Topic = t
	}
	if s.Cfg.Ntfy.Topic == "" {
		s.Cfg.Ntfy.Topic = newNtfyTopic()
	}
	if b.ClearToken {
		s.Cfg.Ntfy.Token = ""
	} else if b.Token != nil {
		s.Cfg.Ntfy.Token = strings.TrimSpace(*b.Token)
	}
	if b.Enabled != nil {
		s.Cfg.Ntfy.Enabled = *b.Enabled
	}
	if b.Kinds != nil {
		if s.Cfg.Ntfy.Off == nil {
			s.Cfg.Ntfy.Off = map[string]bool{}
		}
		for _, k := range NtfyKinds {
			if on, ok := b.Kinds[k.Kind]; ok {
				if on {
					delete(s.Cfg.Ntfy.Off, k.Kind)
				} else {
					s.Cfg.Ntfy.Off[k.Kind] = true
				}
			}
		}
	}
	s.Cfg.mu.Unlock()
	if err := s.Cfg.Save(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	s.ntfyGet(w, r)
}

func (s *Server) ntfyTest(w http.ResponseWriter, r *http.Request) {
	n := s.Cfg.ntfyCopy()
	if n.Topic == "" {
		s.Cfg.ensureNtfyTopic()
		n = s.Cfg.ntfyCopy()
	}
	if !n.Enabled {
		writeJSON(w, 409, map[string]string{"error": "Turn on iPhone alerts first."})
		return
	}
	al := Alert{Kind: "test", Level: "info", Title: "Test notification", Body: "Alerts from " + AppName + " will look like this on your phone."}
	ok, err := defaultNtfy.Send(n, al)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "Couldn't reach the ntfy server. " + err.Error()})
		return
	}
	if !ok {
		writeJSON(w, 429, map[string]string{"error": "A test was just sent. Wait a moment and try again."})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "alert": al})
}

// NtfyPush is hooked from Alerts.Push so every recorded alert can reach ntfy.
func (s *Server) NtfyPush(al Alert) {
	if s == nil || s.Cfg == nil {
		return
	}
	n := s.Cfg.ntfyCopy()
	if _, err := defaultNtfy.Send(n, al); err != nil {
		// Delivery failures are not surfaced in the dashboard; the next alert retries.
		_ = err
	}
}
