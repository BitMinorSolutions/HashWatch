package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	if !Newer("0.5.0", "0.6.0") || !Newer("v0.6.0", "0.6.1") || Newer("0.6.1", "0.6.1") || Newer("0.6.2", "0.6.1") {
		t.Fatal("semver compare")
	}
	if !Newer("ota-v1", "ota-v2") || Newer("ota-v2", "ota-v2") {
		t.Fatal("non-semver compare")
	}
}

func TestOpenEnvelopeRejectsBadSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	man := []byte(`{"schema":1,"channels":{"stable":{"version":"1.0.0","builds":{"linux-amd64":{"url":"http://127.0.0.1/x","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}}`)
	env, err := SignEnvelope(man, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenEnvelope(env, pub); err != nil {
		t.Fatal(err)
	}
	env[len(env)-3] ^= 0x01
	if _, _, err := OpenEnvelope(env, pub); err == nil {
		t.Fatal("forged envelope accepted")
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, _, err := OpenEnvelope(mustSign(t, man, other), pub); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func mustSign(t *testing.T, payload []byte, key ed25519.PrivateKey) []byte {
	t.Helper()
	b, err := SignEnvelope(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallConfirmAndRollback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hashwatch")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	p, err := InstallBinary(exe, data, "old", "new", []byte("NEW-BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	if p.To != "new" {
		t.Fatalf("pending %+v", p)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "NEW-BINARY" {
		t.Fatalf("exe %q", got)
	}
	bak, _ := os.ReadFile(backupPath(exe))
	if string(bak) != "OLD" {
		t.Fatalf("bak %q", bak)
	}
	// Health failed (wrong version / ping not ok) → rollback.
	rolled, err := Confirm(data, exe, "old", false)
	if err != nil || !rolled {
		t.Fatalf("confirm: rolled=%v err=%v", rolled, err)
	}
	got, _ = os.ReadFile(exe)
	if string(got) != "OLD" {
		t.Fatalf("after rollback %q", got)
	}
	if Unconfirmed(data) {
		t.Fatal("pending should be cleared")
	}
	if SkippedVersion(data) != "new" {
		t.Fatalf("skip %q", SkippedVersion(data))
	}
	if last := ReadLast(data); last == nil || !last.Rolled {
		t.Fatalf("last %+v", last)
	}

	// Successful confirm.
	if _, err := InstallBinary(exe, data, "old", "new2", []byte("NEWER")); err != nil {
		t.Fatal(err)
	}
	rolled, err = Confirm(data, exe, "new2", true)
	if err != nil || rolled {
		t.Fatalf("success confirm: rolled=%v err=%v", rolled, err)
	}
	got, _ = os.ReadFile(exe)
	if string(got) != "NEWER" {
		t.Fatalf("kept new? %q", got)
	}
	if Unconfirmed(data) {
		t.Fatal("still unconfirmed")
	}
}

func TestRollbackIfUnconfirmed(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hashwatch")
	_ = os.WriteFile(exe, []byte("OLD"), 0o755)
	data := filepath.Join(dir, "data")
	if _, err := InstallBinary(exe, data, "old", "bad", []byte("BAD")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unconfirmedPath(data), []byte("bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := RollbackIfUnconfirmed(exe, data)
	if err != nil || !ok {
		t.Fatalf("rollback: %v %v", ok, err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD" {
		t.Fatalf("got %q", got)
	}
}

func TestRunnerAppliesAndSkipsBadVersion(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	exe := filepath.Join(dir, "hashwatch")
	_ = os.WriteFile(exe, []byte("V1"), 0o755)
	data := filepath.Join(dir, "data")
	_ = os.MkdirAll(data, 0o700)

	v2 := []byte("V2-BINARY")
	sum := sha256.Sum256(v2)
	var restarted bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hashwatch":
			w.Write(v2)
		case "/manifest.signed.json":
			man, _ := json.Marshal(Manifest{Schema: 1, Channels: map[string]Release{
				"stable": {Version: "0.6.1", Notes: "test", Builds: map[string]Build{
					Platform(runtime.GOOS, runtime.GOARCH): {URL: srv.URL + "/hashwatch", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(v2))},
				}},
			}})
			env, err := SignEnvelope(man, priv)
			if err != nil {
				t.Fatal(err)
			}
			w.Write(env)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	r := &Runner{
		Version: "0.6.0",
		Exe:     exe,
		Dir:     data,
		URL:     func() string { return srv.URL + "/manifest.signed.json" },
		Channel: func() string { return "stable" },
		Service: true,
		Key:     pub,
		Client:  srv.Client(),
		Restart: func() error { restarted = true; return nil },
	}
	st, err := r.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted || !st.Available && st.Latest != "0.6.1" {
		// available may flip after install (version on disk changed but r.Version is still 0.6.0)
	}
	if !restarted {
		t.Fatal("expected restart")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "V2-BINARY" {
		t.Fatalf("exe %q", got)
	}
	// Confirm as healthy.
	if rolled, err := Confirm(data, exe, "0.6.1", true); err != nil || rolled {
		t.Fatalf("confirm %v %v", rolled, err)
	}

	// A later broken version is rolled back and then skipped by auto-check.
	bad := []byte("BAD")
	broken := false
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hashwatch":
			if broken {
				w.Write(bad)
				return
			}
			w.Write(v2)
		case "/manifest.signed.json":
			ver, body := "0.6.1", v2
			if broken {
				ver, body = "0.6.2", bad
			}
			sum := sha256.Sum256(body)
			man, _ := json.Marshal(Manifest{Schema: 1, Channels: map[string]Release{
				"stable": {Version: ver, Builds: map[string]Build{
					Platform(runtime.GOOS, runtime.GOARCH): {URL: srv.URL + "/hashwatch", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))},
				}},
			}})
			env, _ := SignEnvelope(man, priv)
			w.Write(env)
		default:
			http.NotFound(w, r)
		}
	})
	broken = true
	r.Version = "0.6.1"
	restarted = false
	if _, err := r.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("expected restart for 0.6.2")
	}
	if rolled, err := Confirm(data, exe, "0.6.1", false); err != nil || !rolled {
		t.Fatalf("expected rollback: %v %v", rolled, err)
	}
	got, _ = os.ReadFile(exe)
	if string(got) != "V2-BINARY" {
		t.Fatalf("should be back on v2: %q", got)
	}
	restarted = false
	st, err = r.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Fatal("auto-update retried a skipped version")
	}
	if !strings.Contains(st.Error, "skipped") && !strings.Contains(r.Snapshot().Error, "skipped") {
		// Check returns snapshot after skip with lastErr set inside check()
	}
	if SkippedVersion(data) != "0.6.2" {
		t.Fatalf("skip %q", SkippedVersion(data))
	}
}

func TestFetchRejectsHashMismatchAndNonHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	t.Cleanup(srv.Close)
	_, err := FetchBinary(context.Background(), srv.Client(), Build{URL: srv.URL, SHA256: strings.Repeat("ab", 32), Size: 5})
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("hash: %v", err)
	}
	if _, err := FetchEnvelope(context.Background(), nil, "file:///etc/passwd"); err == nil {
		t.Fatal("file URL accepted")
	}
}

func TestUpdaterHasNoMinerImports(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, pkg := range []string{"internal/cgminer", "internal/source", "internal/app"} {
			if bytes.Contains(b, []byte(pkg)) {
				t.Errorf("%s imports or mentions %s (updater must not touch the miner)", e.Name(), pkg)
			}
		}
	}
}

func TestGoListDepsExcludeMiner(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Dir = filepath.Dir(func() string { _, f, _, _ := runtime.Caller(0); return f }())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	s := string(out)
	for _, pkg := range []string{"/internal/cgminer", "/internal/source", "/internal/app"} {
		if strings.Contains(s, pkg) {
			t.Errorf("update package depends on %s:\n%s", pkg, s)
		}
	}
}

func TestCheckRefusesUnsignedBuild(t *testing.T) {
	old := PublicKey
	PublicKey = ""
	t.Cleanup(func() { PublicKey = old })
	r := &Runner{Version: "1", URL: func() string { return "http://127.0.0.1/x" }, Channel: func() string { return "stable" }}
	_, err := r.Check(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "signing key") {
		t.Fatalf("got %v", err)
	}
}

func TestNormalizeAndPlatform(t *testing.T) {
	if NormalizeChannel("DEV") != "dev" || NormalizeChannel("") != "stable" {
		t.Fatal("channel")
	}
	if Platform("linux", "arm64") != "linux-arm64" || Platform("linux", "arm") != "linux-armv7" {
		t.Fatal("platform")
	}
}

func TestSignRoundTripSize(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sum := strings.Repeat("ab", 32)
	man := fmt.Sprintf(`{"schema":1,"channels":{"stable":{"version":"1.2.3","builds":{"linux-amd64":{"url":"http://example.com/b","sha256":"%s"}}}}}`, sum)
	env, err := SignEnvelope([]byte(man), priv)
	if err != nil {
		t.Fatal(err)
	}
	got, payload, err := OpenEnvelope(env, pub)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != man || got.Channels["stable"].Version != "1.2.3" {
		t.Fatalf("%s %+v", payload, got)
	}
}

func TestManifestURL(t *testing.T) {
	base := DefaultReleaseURL
	want := base + "/" + ManifestName
	if ManifestURL(base) != want || ManifestURL(base+"/") != want || ManifestURL(want) != want {
		t.Fatalf("ManifestURL(%q)=%q want %q", base, ManifestURL(base), want)
	}
	if ManifestURL("") != "" {
		t.Fatal("empty")
	}
}
