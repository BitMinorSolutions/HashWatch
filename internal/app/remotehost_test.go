package app

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

func TestViaTailscaleHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"100.64.0.1", true},
		{"100.64.0.1:48412", true},
		{"100.127.255.255:443", true},
		{"100.63.255.255", false},
		{"100.128.0.1", false},
		{"hashwatch.tail123.ts.net", true},
		{"hashwatch.tail123.ts.net:443", true},
		{"HASHWATCH.TAIL123.TS.NET", true},
		{"foo.ts.net.", true},
		{"ts.net", true},
		{"evilts.net", false},
		{"example.ts.net.evil.com", false},
		{"hashwatch.local", false},
		{"hashwatch.local:48412", false},
		{"192.168.1.10", false},
		{"192.168.1.10:48412", false},
		{"127.0.0.1:48412", false},
		{"10.0.0.5", false},
		{"localhost", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = c.host
		if got := ViaTailscaleHost(req); got != c.want {
			t.Fatalf("Host %q: got %v want %v", c.host, got, c.want)
		}
	}
}

func TestRemoteBrandingByHost(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, InstanceID: NewID()}
	h := s.Handler()
	get := func(path, host string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		h.ServeHTTP(rr, req)
		return rr
	}

	lan := get("/", "192.168.1.10:48412")
	if lan.Code != 200 {
		t.Fatalf("lan index: %d", lan.Code)
	}
	if lan.Header().Get("Vary") != "Host" {
		t.Fatalf("lan index missing Vary: Host")
	}
	body := lan.Body.String()
	if !strings.Contains(body, "<title>HashWatch</title>") || strings.Contains(body, "HashWatch Remote") {
		t.Fatalf("lan title: %s", snippet(body, "<title>"))
	}
	if !strings.Contains(body, `content="HashWatch"`) || strings.Contains(body, `content="HashWatch Remote"`) {
		t.Fatalf("lan apple title missing")
	}
	if !strings.Contains(body, `href="/assets/icons/apple-touch-icon.png"`) || strings.Contains(body, "/assets/icons/remote/") {
		t.Fatalf("lan should keep LAN icon paths")
	}
	if !strings.Contains(body, `href="/assets/icons/favicon.png"`) {
		t.Fatalf("lan favicon path")
	}

	local := get("/", "hashwatch.local:48412")
	if strings.Contains(local.Body.String(), "HashWatch Remote") || strings.Contains(local.Body.String(), "/assets/icons/remote/") {
		t.Fatal("hashwatch.local should use the LAN brand")
	}

	for _, host := range []string{"hashwatch.tail1.ts.net", "100.64.12.8:48412", "pi.tailnet.ts.net:443"} {
		rem := get("/", host)
		if rem.Code != 200 {
			t.Fatalf("%s index: %d", host, rem.Code)
		}
		if rem.Header().Get("Vary") != "Host" {
			t.Fatalf("%s index missing Vary: Host", host)
		}
		b := rem.Body.String()
		if !strings.Contains(b, "<title>HashWatch Remote</title>") {
			t.Fatalf("%s title: %s", host, snippet(b, "<title>"))
		}
		if !strings.Contains(b, `content="HashWatch Remote"`) {
			t.Fatalf("%s apple-mobile-web-app-title", host)
		}
		if !strings.Contains(b, `href="/assets/icons/remote/apple-touch-icon.png"`) || !strings.Contains(b, `href="/assets/icons/remote/favicon.png"`) {
			t.Fatalf("%s icon paths: %s", host, snippet(b, "apple-touch-icon"))
		}
		if strings.Contains(b, `href="/assets/icons/apple-touch-icon.png"`) {
			t.Fatalf("%s still points at the LAN apple-touch-icon", host)
		}
	}

	mlan := get("/manifest.webmanifest", "hashwatch.local")
	mrem := get("/manifest.webmanifest", "hashwatch.tail1.ts.net")
	if mlan.Code != 200 || mrem.Code != 200 {
		t.Fatalf("manifest codes %d %d", mlan.Code, mrem.Code)
	}
	if mlan.Header().Get("Vary") != "Host" || mrem.Header().Get("Vary") != "Host" {
		t.Fatal("manifest should Vary: Host")
	}
	if !strings.Contains(mlan.Body.String(), `"name": "HashWatch"`) || strings.Contains(mlan.Body.String(), "HashWatch Remote") {
		t.Fatalf("lan manifest: %s", mlan.Body.String())
	}
	if !strings.Contains(mlan.Body.String(), `"/assets/icons/icon-192.png"`) || strings.Contains(mlan.Body.String(), "/assets/icons/remote/") {
		t.Fatalf("lan manifest icons: %s", mlan.Body.String())
	}
	if !strings.Contains(mrem.Body.String(), `"name": "HashWatch Remote"`) || !strings.Contains(mrem.Body.String(), `"short_name": "HashWatch Remote"`) {
		t.Fatalf("remote manifest name: %s", mrem.Body.String())
	}
	if !strings.Contains(mrem.Body.String(), `"/assets/icons/remote/icon-192.png"`) || !strings.Contains(mrem.Body.String(), `"/assets/icons/remote/icon-512.png"`) || !strings.Contains(mrem.Body.String(), `"/assets/icons/remote/maskable-512.png"`) {
		t.Fatalf("remote manifest icons: %s", mrem.Body.String())
	}

	ilan := get("/assets/icons/apple-touch-icon.png", "hashwatch.local")
	irem := get("/assets/icons/remote/apple-touch-icon.png", "hashwatch.tail1.ts.net")
	if ilan.Code != 200 || irem.Code != 200 {
		t.Fatalf("icon codes %d %d", ilan.Code, irem.Code)
	}
	if ilan.Header().Get("Vary") != "Host" || irem.Header().Get("Vary") != "Host" {
		t.Fatal("asset icons should Vary: Host")
	}
	if bytes.Equal(ilan.Body.Bytes(), irem.Body.Bytes()) {
		t.Fatal("LAN and remote apple-touch-icon are identical")
	}

	r1 := get("/apple-touch-icon.png", "hashwatch.local")
	r2 := get("/apple-touch-icon.png", "pi.ts.net")
	if r1.Code != 200 || r2.Code != 200 {
		t.Fatalf("root touch-icon codes %d %d", r1.Code, r2.Code)
	}
	if r1.Header().Get("Vary") != "Host" || r2.Header().Get("Vary") != "Host" {
		t.Fatal("root apple-touch-icon should Vary: Host")
	}
	if bytes.Equal(r1.Body.Bytes(), r2.Body.Bytes()) {
		t.Fatal("root apple-touch-icon was cached/identical across hosts")
	}
	if !bytes.Equal(r1.Body.Bytes(), ilan.Body.Bytes()) || !bytes.Equal(r2.Body.Bytes(), irem.Body.Bytes()) {
		t.Fatal("root apple-touch-icon should match the host-specific asset")
	}

	f1 := get("/favicon.ico", "192.168.1.10")
	f2 := get("/favicon.ico", "100.64.1.2")
	if f1.Code != 200 || f2.Code != 200 || bytes.Equal(f1.Body.Bytes(), f2.Body.Bytes()) {
		t.Fatalf("favicon.ico should differ by host (%d %d equal=%v)", f1.Code, f2.Code, bytes.Equal(f1.Body.Bytes(), f2.Body.Bytes()))
	}
}

func snippet(s, key string) string {
	i := strings.Index(s, key)
	if i < 0 {
		if len(s) > 240 {
			return s[:240]
		}
		return s
	}
	j := i + 160
	if j > len(s) {
		j = len(s)
	}
	return s[i:j]
}
