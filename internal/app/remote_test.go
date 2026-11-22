package app

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/tunnel"
)

func fakeCloudflared(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "cloudflared")
	os.WriteFile(p, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo cloudflared version 2026.11.0; exit 0; fi\necho 'INF |  https://test-link-abc.trycloudflare.com  |' >&2\necho 'INF Registered tunnel connection connIndex=0' >&2\nexec sleep 30\n"), 0o755)
	return p
}

func TestRemoteAccessGuards(t *testing.T) {
	t.Setenv("HASHWATCH_CLOUDFLARED", fakeCloudflared(t))
	ts, s := newTestServer(t, nil, false, "")
	s.Tunnel = &tunnel.Manager{Dir: t.TempDir(), Origin: ts.URL, OnURL: s.TunnelURL}
	defer s.Tunnel.Stop()
	s.Alerts = NewAlerts(t.TempDir())

	// no password yet: enabling must ask for one
	if r := post(t, ts, "/api/remote", `{"enabled":true}`, nil); r.StatusCode != 412 {
		t.Fatalf("want 412, got %d", r.StatusCode)
	}
	// through the tunnel (Cloudflare headers) admin settings are refused
	req, _ := http.NewRequest("POST", ts.URL+"/api/remote", strings.NewReader(`{"enabled":true,"password":"secret1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cf-Ray", "x")
	req.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
	attachCSRF(t, ts, req)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 403 {
		t.Fatalf("tunnel admin: want 403, got %d", r.StatusCode)
	}
	r := post(t, ts, "/api/remote", `{"enabled":true,"password":"secret1"}`, nil)
	if r.StatusCode != 200 || s.Cfg.PasswordHash == "" || !s.Cfg.Remote.Enabled {
		t.Fatalf("enable: %d", r.StatusCode)
	}
	var ck *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == "hw_session" {
			ck = c
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Tunnel.Status().State != "online" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if u := s.Tunnel.Status().URL; u != "https://test-link-abc.trycloudflare.com" {
		t.Fatalf("url %q (%+v)", u, s.Tunnel.Status())
	}
	// QR only for our own link
	get := func(path string) *http.Response {
		rq, _ := http.NewRequest("GET", ts.URL+path, nil)
		rq.AddCookie(ck)
		rr, _ := http.DefaultClient.Do(rq)
		return rr
	}
	if rr := get("/api/remote/qr.svg?u=https://evil.example"); rr.StatusCode != 404 {
		t.Fatalf("foreign QR: %d", rr.StatusCode)
	}
	if rr := get("/api/remote/qr.svg?u=https://test-link-abc.trycloudflare.com"); rr.StatusCode != 404 {
		t.Fatalf("QR of the admin address: %d", rr.StatusCode)
	}
	share := s.shareURL()
	if !strings.HasPrefix(share, "https://test-link-abc.trycloudflare.com/v/") {
		t.Fatalf("share url %q", share)
	}
	rr := get("/api/remote/qr.svg?u=" + url.QueryEscape(share))
	b, _ := io.ReadAll(rr.Body)
	if rr.StatusCode != 200 || !strings.HasPrefix(string(b), "<svg") {
		t.Fatalf("qr: %d", rr.StatusCode)
	}
	// can't drop the password while remote access is on
	if r := post(t, ts, "/api/password", `{"current":"secret1","new":""}`, ck); r.StatusCode != 409 {
		t.Fatalf("password clear: want 409, got %d", r.StatusCode)
	}
	// a changed link raises an alert
	s.TunnelURL("https://other-link.trycloudflare.com")
	if items, _ := s.Alerts.Since(0, 0); len(items) != 1 || items[0].Kind != "link" {
		t.Fatalf("link alert: %+v", items)
	}
	if r := post(t, ts, "/api/remote", `{"enabled":false}`, ck); r.StatusCode != 200 || s.Tunnel.Running() {
		t.Fatal("disable failed")
	}
}

func TestRemoteLockedInPreview(t *testing.T) {
	ts, s := newTestServer(t, nil, true, "")
	s.Tunnel = &tunnel.Manager{Dir: t.TempDir()}
	if r := post(t, ts, "/api/remote", `{"enabled":true,"password":"secret1"}`, nil); r.StatusCode != 403 {
		t.Fatalf("preview: want 403, got %d", r.StatusCode)
	}
}

func TestExpectedSHA(t *testing.T) {
	body := "SHA256 Checksums:\n```\ncloudflared-linux-amd64: " + strings.Repeat("a", 64) + "\n```"
	if h, err := tunnel.ExpectedSHA(body, "sha256:"+strings.Repeat("a", 64), "cloudflared-linux-amd64"); err != nil || h != strings.Repeat("a", 64) {
		t.Fatal(h, err)
	}
	if _, err := tunnel.ExpectedSHA(body, "sha256:"+strings.Repeat("b", 64), "cloudflared-linux-amd64"); err == nil {
		t.Fatal("disagreeing checksums accepted")
	}
	if _, err := tunnel.ExpectedSHA("", "", "cloudflared-linux-amd64"); err == nil {
		t.Fatal("missing checksum accepted")
	}
}
