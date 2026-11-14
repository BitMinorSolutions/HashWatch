package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashwatch/hashwatch/web"
)

func writeFakeTailscale(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tailscale")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HASHWATCH_TAILSCALE", bin)
	resetTailscaleCache()
	return bin
}

func TestLookupTailscaleFromFakeBinary(t *testing.T) {
	writeFakeTailscale(t, `
if [ "$1" = ip ]; then echo 100.64.12.8; exit 0; fi
if [ "$1" = status ]; then echo '{"Self":{"DNSName":"hashwatch.tailnet.ts.net.","Online":true}}'; exit 0; fi
if [ "$1" = serve ]; then
  if [ "$2" = status ]; then echo '{"Web":{"hashwatch.tailnet.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:48412"}}}}}'; exit 0; fi
  echo served; exit 0
fi
exit 1
`)
	info := LookupTailscale(48412)
	if !info.Available || info.IP != "100.64.12.8" || info.Name != "hashwatch.tailnet.ts.net" {
		t.Fatalf("info %+v", info)
	}
	if info.HTTP != "http://hashwatch.tailnet.ts.net:48412" {
		t.Fatalf("http %q", info.HTTP)
	}
	if !info.Serve || info.HTTPS != "https://hashwatch.tailnet.ts.net" || info.URL != info.HTTPS {
		t.Fatalf("serve %+v", info)
	}
}

func TestLookupTailscaleMissing(t *testing.T) {
	t.Setenv("HASHWATCH_TAILSCALE", "/no/such/tailscale")
	resetTailscaleCache()
	info := LookupTailscale(48412)
	if info.Available {
		t.Fatalf("expected unavailable: %+v", info)
	}
}

func TestServeHTTPSParse(t *testing.T) {
	u := serveHTTPS(`{"Web":{"pi.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:48412"}}}}}`, "pi.example.ts.net")
	if u != "https://pi.example.ts.net" {
		t.Fatalf("got %q", u)
	}
}

func TestTailscaleAPIAndQR(t *testing.T) {
	writeFakeTailscale(t, `
if [ "$1" = ip ]; then echo 100.64.1.2; exit 0; fi
if [ "$1" = status ]; then echo '{"Self":{"DNSName":"box.ts.net."}}'; exit 0; fi
echo '{}'; exit 0
`)
	cfg, _ := LoadConfig(t.TempDir())
	cfg.SetPassword("mainpass1")
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), 0), Static: web.FS, Port: 48412, InstanceID: NewID()}
	ts := newTestServerFrom(t, s)
	c := &tclient{t: t, ts: ts}
	if r, _ := c.do("POST", "/api/login", `{"password":"mainpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}
	r, out := c.do("GET", "/api/tailscale", "")
	if r.StatusCode != 200 || out["ip"] != "100.64.1.2" || out["name"] != "box.ts.net" {
		t.Fatalf("get %d %v", r.StatusCode, out)
	}
	url, _ := out["url"].(string)
	if !strings.Contains(url, "box.ts.net:48412") && !strings.Contains(url, "100.64.1.2:48412") {
		t.Fatalf("url %q", url)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/tailscale/qr.svg?u="+url, nil)
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "svg") || !strings.Contains(string(body), "<svg") {
		t.Fatalf("qr %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/tailscale/qr.svg?u=http://evil.example/", nil)
	if c.ck != nil {
		req.AddCookie(c.ck)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("foreign url qr: %d", res.StatusCode)
	}
}
