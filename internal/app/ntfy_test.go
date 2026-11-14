package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

type ntfyReq struct {
	Title, Priority, Auth, Body, Path string
}

func mockNtfy(t *testing.T) (*httptest.Server, *[]ntfyReq, *sync.Mutex) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []ntfyReq
	)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, ntfyReq{
			Title:    r.Header.Get("Title"),
			Priority: r.Header.Get("Priority"),
			Auth:     r.Header.Get("Authorization"),
			Body:     string(b),
			Path:     r.URL.Path,
		})
		mu.Unlock()
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"ok"}`))
	}))
	t.Cleanup(h.Close)
	return h, &got, &mu
}

func (s *ntfySender) reset() {
	s.mu.Lock()
	s.last, s.sent = map[string]time.Time{}, nil
	s.mu.Unlock()
}

func TestNtfyPrioritiesAndNoCredentialsInBody(t *testing.T) {
	h, got, mu := mockNtfy(t)
	snd := &ntfySender{client: h.Client(), last: map[string]time.Time{}, now: time.Now}
	cfg := NtfySettings{Enabled: true, Server: h.URL, Topic: "hw" + strings.Repeat("a", 30), Token: "super-secret-token"}
	cases := []struct {
		kind, pri string
	}{
		{"block", "5"},
		{"offline", "4"},
		{"temp", "4"},
		{"hashrate", "3"},
		{"summary", "3"},
	}
	for _, c := range cases {
		snd.reset()
		al := Alert{Kind: c.kind, Title: "Hello", Body: "All good", MinerName: "Nano"}
		ok, err := snd.Send(cfg, al)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", c.kind, ok, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != len(cases) {
		t.Fatalf("got %d posts, want %d", len(*got), len(cases))
	}
	for i, c := range cases {
		r := (*got)[i]
		if r.Priority != c.pri {
			t.Errorf("%s priority %q, want %q", c.kind, r.Priority, c.pri)
		}
		if r.Auth != "Bearer super-secret-token" {
			t.Errorf("%s auth %q", c.kind, r.Auth)
		}
		if strings.Contains(r.Body, "super-secret-token") || strings.Contains(r.Title, "super-secret-token") {
			t.Errorf("%s leaked token: %+v", c.kind, r)
		}
		if r.Path != "/"+cfg.Topic {
			t.Errorf("path %s", r.Path)
		}
	}
}

func TestNtfyScrubsTokenFromMessage(t *testing.T) {
	h, got, mu := mockNtfy(t)
	snd := &ntfySender{client: h.Client(), last: map[string]time.Time{}, now: time.Now}
	cfg := NtfySettings{Enabled: true, Server: h.URL, Topic: "hw" + strings.Repeat("b", 30), Token: "tok_LEAK"}
	ok, err := snd.Send(cfg, Alert{Kind: "offline", Title: "x tok_LEAK", Body: "body tok_LEAK"})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	mu.Lock()
	defer mu.Unlock()
	r := (*got)[0]
	if strings.Contains(r.Title, "tok_LEAK") || strings.Contains(r.Body, "tok_LEAK") {
		t.Fatalf("token in payload: %+v", r)
	}
}

func TestNtfyDedupeAndRateLimit(t *testing.T) {
	h, got, mu := mockNtfy(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	snd := &ntfySender{client: h.Client(), last: map[string]time.Time{}, now: func() time.Time { return now }}
	cfg := NtfySettings{Enabled: true, Server: h.URL, Topic: "hw" + strings.Repeat("c", 30)}
	al := Alert{Kind: "offline", Title: "Nano is offline", Body: "Can't reach it.", Miner: "m1"}
	ok, err := snd.Send(cfg, al)
	if err != nil || !ok {
		t.Fatalf("first: %v %v", ok, err)
	}
	ok, err = snd.Send(cfg, al)
	if err != nil || ok {
		t.Fatalf("duplicate should be dropped, ok=%v err=%v", ok, err)
	}
	// different body, same kind: allowed once more, then rate-limited
	al2 := al
	al2.Body = "Still down."
	ok, _ = snd.Send(cfg, al2)
	if !ok {
		t.Fatal("second distinct offline should send")
	}
	al3 := al
	al3.Body = "Third"
	ok, _ = snd.Send(cfg, al3)
	if ok {
		t.Fatal("third offline in 10 min should be rate-limited")
	}
	// block is not capped the same way, but exact dups still drop
	blk := Alert{Kind: "block", Title: "Block found!", Body: "Found Blocks went up.", Miner: "m1"}
	ok, _ = snd.Send(cfg, blk)
	if !ok {
		t.Fatal("block should send")
	}
	ok, _ = snd.Send(cfg, blk)
	if ok {
		t.Fatal("duplicate block should drop")
	}
	mu.Lock()
	n := len(*got)
	mu.Unlock()
	if n != 3 { // first offline, second offline, one block
		t.Fatalf("posts=%d want 3", n)
	}
}

func TestNtfyKindToggleAndDisabled(t *testing.T) {
	h, got, mu := mockNtfy(t)
	snd := &ntfySender{client: h.Client(), last: map[string]time.Time{}, now: time.Now}
	cfg := NtfySettings{Enabled: true, Server: h.URL, Topic: "hw" + strings.Repeat("d", 30), Off: map[string]bool{"hashrate": true}}
	ok, err := snd.Send(cfg, Alert{Kind: "hashrate", Title: "Drop", Body: "low"})
	if err != nil || ok {
		t.Fatalf("toggled off should not send: %v %v", ok, err)
	}
	cfg.Enabled = false
	ok, err = snd.Send(cfg, Alert{Kind: "block", Title: "Block found!", Body: "x"})
	if err != nil || ok {
		t.Fatalf("disabled should not send: %v %v", ok, err)
	}
	mu.Lock()
	n := len(*got)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("unexpected posts: %d", n)
	}
}

func TestNormalizeNtfyServer(t *testing.T) {
	u, err := normalizeNtfyServer("")
	if err != nil || u != DefaultNtfyServer {
		t.Fatalf("default %q %v", u, err)
	}
	if _, err := normalizeNtfyServer("https://user:pass@ntfy.example/x"); err == nil {
		t.Fatal("userinfo must be rejected")
	}
	if _, err := normalizeNtfyServer("javascript:alert(1)"); err == nil {
		t.Fatal("bad scheme")
	}
}

func TestNtfyAPIAndQRAndTestButton(t *testing.T) {
	h, got, mu := mockNtfy(t)
	defaultNtfy.reset()
	defaultNtfy.client = h.Client()
	t.Cleanup(func() {
		defaultNtfy.reset()
		defaultNtfy.client = &http.Client{Timeout: 8 * time.Second}
	})

	cfg, _ := LoadConfig(t.TempDir())
	cfg.SetPassword("mainpass1")
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), 0), Static: web.FS, Port: 48412, InstanceID: NewID(), Alerts: NewAlerts(t.TempDir())}
	s.Alerts.Push = s.NtfyPush
	ts := newTestServerFrom(t, s)
	c := &tclient{t: t, ts: ts}
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	r, out := c.do("GET", "/api/ntfy", "")
	if r.StatusCode != 200 {
		t.Fatalf("get %d %v", r.StatusCode, out)
	}
	if out["token"] != nil {
		t.Fatalf("token must not appear in GET: %v", out)
	}
	topic, _ := out["topic"].(string)
	if !validNtfyTopic(topic) {
		t.Fatalf("topic %q", topic)
	}
	if out["token_set"] != false {
		t.Fatalf("token_set %v", out["token_set"])
	}
	body, _ := json.Marshal(map[string]any{
		"enabled": true,
		"server":  h.URL,
		"token":   "api-token-secret",
		"kinds":   map[string]bool{"block": true, "offline": true, "hashrate": true, "temp": true, "summary": true},
	})
	r, out = c.do("POST", "/api/ntfy", string(body))
	if r.StatusCode != 200 {
		t.Fatalf("set %d %v", r.StatusCode, out)
	}
	if out["token_set"] != true {
		t.Fatalf("token_set after save: %v", out)
	}
	if out["token"] != nil {
		t.Fatalf("token leaked after save: %v", out)
	}
	sub, _ := out["subscribe_url"].(string)
	if !strings.HasPrefix(sub, h.URL+"/") || strings.Contains(sub, "api-token-secret") {
		t.Fatalf("subscribe %q", sub)
	}

	req, _ := http.NewRequest("GET", ts.URL+"/api/ntfy/qr.svg?u="+sub, nil)
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "<svg") {
		t.Fatalf("qr %d", res.StatusCode)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/ntfy/qr.svg?u=https://evil.example/nope", nil)
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("foreign qr %d", res.StatusCode)
	}

	r, out = c.do("POST", "/api/ntfy/test", `{}`)
	if r.StatusCode != 200 {
		t.Fatalf("test %d %v", r.StatusCode, out)
	}
	mu.Lock()
	if len(*got) != 1 {
		t.Fatalf("test posts=%d", len(*got))
	}
	tr := (*got)[0]
	mu.Unlock()
	if tr.Priority != "3" || strings.Contains(tr.Body, "api-token-secret") || strings.Contains(tr.Title, "api-token-secret") {
		t.Fatalf("test payload %+v", tr)
	}
	if tr.Auth != "Bearer api-token-secret" {
		t.Fatalf("auth %q", tr.Auth)
	}

	// Fire a block through the Push hook
	s.Alerts.Fire(Alert{Kind: "block", Level: "celebrate", Title: "Block found!", Body: "Found Blocks went from 0 to 1.", MinerName: "Nano"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(*got)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for ntfy push, posts=%d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	var blk ntfyReq
	for _, x := range *got {
		if x.Priority == "5" {
			blk = x
		}
	}
	mu.Unlock()
	if blk.Title == "" || strings.Contains(blk.Body, "api-token-secret") {
		t.Fatalf("block payload %+v", blk)
	}
}
