package tunnel

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// Real download from Cloudflare's GitHub releases (set HW_REAL_DOWNLOAD=1).
func TestRealDownload(t *testing.T) {
	if os.Getenv("HW_REAL_DOWNLOAD") == "" {
		t.Skip("network test")
	}
	m := &Manager{Dir: t.TempDir()}
	bin, err := m.ensureBinary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	t.Logf("%s %s progress=%.2f", bin, out, m.Status().Progress)
	if err != nil {
		t.Fatal(err)
	}
	// cached: second call must not download again
	m.st = Status{}
	if _, err := m.ensureBinary(context.Background()); err != nil || m.Status().State == "downloading" {
		t.Fatal("expected cache hit", err)
	}
	// tampered binary is re-downloaded, not trusted
	os.WriteFile(bin, []byte("tampered"), 0o755)
	if b2, err := m.ensureBinary(context.Background()); err != nil {
		t.Fatal(err)
	} else if out, _ := exec.Command(b2, "--version").CombinedOutput(); len(out) < 10 {
		t.Fatal("tampered binary kept")
	}
}
