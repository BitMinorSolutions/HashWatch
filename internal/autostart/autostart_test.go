package autostart

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestEnableDisableFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the registry")
	}
	h := t.TempDir()
	s := Spec{Exe: "/opt/Hash watch/hashwatch", Args: []string{"-no-browser", "-config", "/x y/cfg"}, Dir: h, Home: h, AppName: "Hashwatch"}
	st, err := Enable(s)
	if err != nil || !st.Enabled {
		t.Fatalf("enable: %v %+v", err, st)
	}
	b, _ := os.ReadFile(st.Path)
	if !strings.Contains(string(b), "-no-browser") || !strings.Contains(string(b), "Hash watch") {
		t.Fatalf("bad file: %s", b)
	}
	if st, _ = Disable(s); st.Enabled {
		t.Fatal("still enabled")
	}
}
