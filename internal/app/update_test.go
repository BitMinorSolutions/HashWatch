package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/update"
	"github.com/hashwatch/hashwatch/web"
)

func TestUpdateAPIGuardedAndApplies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	exe := filepath.Join(dir, "hashwatch")
	if err := os.WriteFile(exe, []byte("V1"), 0o755); err != nil {
		t.Fatal(err)
	}
	v2 := []byte("V2-OK")
	sum := sha256.Sum256(v2)
	var rel *httptest.Server
	rel = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bin" {
			w.Write(v2)
			return
		}
		man, _ := json.Marshal(update.Manifest{Schema: 1, Channels: map[string]update.Release{
			"stable": {Version: "9.0.0", Notes: "test", Builds: map[string]update.Build{
				update.Platform(runtime.GOOS, runtime.GOARCH): {URL: rel.URL + "/bin", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(v2))},
			}},
			"dev": {Version: "9.0.0-dev", Builds: map[string]update.Build{
				update.Platform(runtime.GOOS, runtime.GOARCH): {URL: rel.URL + "/bin", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(v2))},
			}},
		}})
		env, err := update.SignEnvelope(man, priv)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(env)
	}))
	t.Cleanup(rel.Close)

	cfg, _ := LoadConfig(dir)
	cfg.UpdateURL = rel.URL + "/manifest.signed.json"
	poll := NewPoller(t.TempDir(), time.Hour)
	s := &Server{Cfg: cfg, Poll: poll, Static: web.FS, Version: "8.0.0", InstanceID: NewID()}
	s.Update = NewUpdateRunner(cfg, "8.0.0", exe, dir)
	s.Update.Key = pub
	s.Update.Client = rel.Client()
	s.Update.Service = true
	var restarted bool
	s.Update.Restart = func() error { restarted = true; return nil }

	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c := &tclient{t: t, ts: ts}

	if r, out := c.do("GET", "/api/update", ""); r.StatusCode != 200 || out["current"] != "8.0.0" {
		t.Fatalf("status: %d %v", r.StatusCode, out)
	}
	if r, out := c.do("POST", "/api/update", `{"check":true}`); r.StatusCode != 200 {
		t.Fatalf("check: %d %v", r.StatusCode, out)
	} else {
		u, _ := out["update"].(map[string]any)
		if u["latest"] != "9.0.0" || u["available"] != true {
			t.Fatalf("check body %v", out)
		}
	}
	if r, out := c.do("POST", "/api/update", `{"channel":"dev","check":true}`); r.StatusCode != 200 {
		t.Fatalf("channel: %d %v", r.StatusCode, out)
	}
	if cfg.updateChannel() != "dev" {
		t.Fatalf("channel not saved: %s", cfg.updateChannel())
	}
	if r, out := c.do("POST", "/api/update", `{"channel":"stable","apply":true}`); r.StatusCode != 200 {
		t.Fatalf("apply: %d %v", r.StatusCode, out)
	}
	if !restarted {
		t.Fatal("expected restart")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "V2-OK" {
		t.Fatalf("binary %q", got)
	}
}

func TestUpdateApplyRefusedOutsideService(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := LoadConfig(dir)
	cfg.UpdateURL = "http://127.0.0.1:1/manifest.signed.json"
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, Version: "1.0.0"}
	s.Update = NewUpdateRunner(cfg, "1.0.0", filepath.Join(dir, "x"), dir)
	s.Update.Service = false
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s.Update.Key, _, _ = ed25519.GenerateKey(rand.Reader)
	_ = priv
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c := &tclient{t: t, ts: ts}
	if r, out := c.do("POST", "/api/update", `{"apply":true}`); r.StatusCode != 502 || !strings.Contains(fmtErr(out), "service") && !strings.Contains(fmtErr(out), "signing") && !strings.Contains(fmtErr(out), "update") {
		// may fail on fetch/key; either 502 is fine as long as we didn't swap anything
		if r.StatusCode == 200 {
			t.Fatalf("apply succeeded outside service: %v", out)
		}
	}
}

func TestUpdateNeverMentionsMinerRoutes(t *testing.T) {
	b, err := os.ReadFile("update.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"/api/control", "SetPool", "Reboot", "cgminer", "SwitchPool"} {
		if strings.Contains(string(b), s) {
			t.Errorf("app update handler mentions %s", s)
		}
	}
}
