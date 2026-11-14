package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/web"
)

// foreignDashboard simulates another program (like an existing miner dashboard) on 127.0.0.1.
func foreignDashboard(t *testing.T) (port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ping" { // even a look-alike answer must not be trusted
			fmt.Fprint(w, `{"app":"HashWatch","id":"deadbeef0000"}`)
			return
		}
		fmt.Fprint(w, "<html>Avalon Nano 3 · Miner API error: [Errno 65] No route to host</html>")
	})
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func get(t *testing.T, url string) string {
	t.Helper()
	r, err := (&http.Client{Timeout: 2 * time.Second}).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// A foreign HTTP server on the preferred port: HashWatch must pick another port, its own
// local URL must reach HashWatch (not the other app), and no hand-off may happen.
func TestForeignServerOnDefaultPort(t *testing.T) {
	foreign := foreignDashboard(t)
	dir := t.TempDir()

	// 1. A stale/forged instance.json pointing at the foreign server is not a running HashWatch.
	b, _ := json.Marshal(instanceFile{PID: 1, Port: foreign, ID: "deadbeef0000"})
	os.WriteFile(filepath.Join(dir, "instance.json"), b, 0o600)
	if u, ok := RunningInstance(dir); ok {
		t.Fatalf("handed off to a foreign server: %s", u)
	}

	// 2. Binding with the foreign port preferred (wildcard host, like the real app) picks another port.
	for _, host := range []string{"0.0.0.0", "127.0.0.1"} {
		ln, port, err := Listen(host, PortCandidates(0, 0, foreign))
		if err != nil {
			t.Fatal(err)
		}
		if port == foreign {
			t.Fatalf("%s: bound the foreign program's port %d", host, port)
		}
		cfg, _ := LoadConfig(t.TempDir())
		s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS, InstanceID: NewID(), Port: port}
		hs := &http.Server{Handler: s.Handler()}
		go hs.Serve(ln)
		if body := get(t, LocalURL(port)+"/api/ping"); !strings.Contains(body, AppID) || !strings.Contains(body, s.InstanceID) {
			t.Fatalf("%s: own URL did not reach HashWatch: %s", host, body)
		}
		// 3. The real running copy is recognised; the foreign one still is not.
		WriteInstance(dir, port, s.InstanceID)
		if u, ok := RunningInstance(dir); !ok || u != LocalURL(port) {
			t.Fatalf("%s: own instance not recognised: %q %v", host, u, ok)
		}
		RemoveInstance(dir, s.InstanceID)
		hs.Close()
	}
	// The foreign program was left alone.
	if !strings.Contains(get(t, LocalURL(foreign)+"/"), "Avalon Nano 3") {
		t.Fatal("foreign server disturbed")
	}
}

// macOS can let a wildcard bind succeed while another program holds 127.0.0.1:port, so
// local traffic goes elsewhere. The post-bind probe must detect that shadowing.
func TestShadowedPortDetected(t *testing.T) {
	foreign := foreignDashboard(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if reachesUs(ln, "0.0.0.0", foreign) { // probe lands on the foreign program, not ln
		t.Fatal("shadowed port reported as ours")
	}
	if !reachesUs(ln, "0.0.0.0", ln.Addr().(*net.TCPAddr).Port) {
		t.Fatal("own port not reported as ours")
	}
	if !portAnswers("0.0.0.0", foreign) {
		t.Fatal("foreign port not seen as busy")
	}
}

func TestPortCandidates(t *testing.T) {
	c := PortCandidates(0, 8787, DefaultPort) // 0.5.0 config: legacy port ignored
	if c[0] != DefaultPort || len(c) != 10 {
		t.Fatalf("legacy migration: %v", c)
	}
	c = PortCandidates(0, 50123, DefaultPort) // last used (auto-picked) port comes first
	if c[0] != 50123 || c[1] != DefaultPort {
		t.Fatalf("saved: %v", c)
	}
	c = PortCandidates(9000, 50123, DefaultPort)
	if c[0] != 9000 || c[1] != 50123 {
		t.Fatalf("explicit: %v", c)
	}
	if DefaultPort == 8787 || strconv.Itoa(DefaultPort) == "" {
		t.Fatal("default port")
	}
}
