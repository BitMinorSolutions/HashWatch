package tunnel

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// cloudflared that prints a link and dies after a moment: the manager must restart it.
func TestRestartsWhenItDies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	p := filepath.Join(t.TempDir(), "cloudflared")
	os.WriteFile(p, []byte("#!/bin/sh\n[ \"$1\" = --version ] && exit 0\necho \"INF https://dies-$$.trycloudflare.com\" >&2\necho \"INF Registered tunnel connection\" >&2\nexec sleep 0.3\n"), 0o755)
	t.Setenv("HASHWATCH_CLOUDFLARED", p)
	var urls []string
	m := &Manager{Dir: t.TempDir(), Origin: "http://127.0.0.1:1", OnURL: func(u string) { urls = append(urls, u) }}
	m.Start()
	time.Sleep(3500 * time.Millisecond)
	m.Stop()
	if len(urls) < 2 {
		t.Fatalf("expected a restart with a new link, got %v", urls)
	}
	if urls[0] == urls[1] {
		t.Fatal("expected distinct links")
	}
}

func TestAssetNames(t *testing.T) {
	for _, c := range [][3]string{{"linux", "amd64", "cloudflared-linux-amd64"}, {"linux", "arm", "cloudflared-linux-armhf"}, {"linux", "arm64", "cloudflared-linux-arm64"},
		{"darwin", "arm64", "cloudflared-darwin-arm64.tgz"}, {"darwin", "amd64", "cloudflared-darwin-amd64.tgz"}, {"windows", "amd64", "cloudflared-windows-amd64.exe"}} {
		if n, err := AssetName(c[0], c[1]); err != nil || n != c[2] {
			t.Fatal(c, n, err)
		}
	}
}

func TestNotOnlineUntilRegistered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	p := filepath.Join(t.TempDir(), "cloudflared")
	os.WriteFile(p, []byte("#!/bin/sh\n[ \"$1\" = --version ] && exit 0\necho \"INF https://wait-here.trycloudflare.com\" >&2\nexec sleep 5\n"), 0o755)
	t.Setenv("HASHWATCH_CLOUDFLARED", p)
	m := &Manager{Dir: t.TempDir(), Origin: "http://127.0.0.1:1"}
	m.Start()
	defer m.Stop()
	time.Sleep(700 * time.Millisecond)
	if st := m.Status(); st.State == "online" || st.URL != "https://wait-here.trycloudflare.com" {
		t.Fatalf("%+v", st)
	}
}
