package install

import (
	"os"
	"path/filepath"
	"testing"
)

func mkApp(t *testing.T, dir string) string {
	app := filepath.Join(dir, "Hashwatch.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "Hashwatch"), []byte("bin-v2"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("<plist/>"), 0o644)
	return app
}

func TestCheck(t *testing.T) {
	t.Setenv("HASHWATCH_APPLICATIONS_DIR", "/Applications")
	cases := map[string]string{
		"/Users/a/Downloads/Hashwatch.app/Contents/MacOS/Hashwatch":                              "downloads",
		"/Volumes/Hashwatch/Hashwatch.app/Contents/MacOS/Hashwatch":                              "disk image",
		"/private/var/folders/x/T/AppTranslocation/ABC/d/Hashwatch.app/Contents/MacOS/Hashwatch": "translocated",
		"/Users/a/Desktop/Hashwatch.app/Contents/MacOS/Hashwatch":                                "elsewhere",
	}
	for exe, where := range cases {
		if in := Check(exe, "/Users/a"); !in.Offer || in.Where != where || in.Dest != "/Applications/Hashwatch.app" {
			t.Errorf("%s: %+v", exe, in)
		}
	}
	for _, exe := range []string{"/Applications/Hashwatch.app/Contents/MacOS/Hashwatch", "/Users/a/Applications/Hashwatch.app/Contents/MacOS/Hashwatch", "/usr/local/bin/hashwatch"} {
		if Check(exe, "/Users/a").Offer {
			t.Errorf("%s: should not offer", exe)
		}
	}
}

func TestMoveReplacesAndKeepsExecBit(t *testing.T) {
	src := mkApp(t, t.TempDir())
	apps := t.TempDir()
	old := mkApp(t, apps)
	os.WriteFile(filepath.Join(old, "Contents", "MacOS", "Hashwatch"), []byte("bin-v1"), 0o755)
	dst, err := Move(src, apps)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "Contents", "MacOS", "Hashwatch"))
	st, _ := os.Stat(filepath.Join(dst, "Contents", "MacOS", "Hashwatch"))
	if string(b) != "bin-v2" || st.Mode()&0o111 == 0 {
		t.Fatalf("got %q mode %v", b, st.Mode())
	}
	ents, _ := os.ReadDir(apps)
	if len(ents) != 1 {
		t.Fatalf("leftovers in Applications: %v", ents)
	}
}

func TestMoveNoPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	src := mkApp(t, t.TempDir())
	apps := t.TempDir()
	os.Chmod(apps, 0o555)
	defer os.Chmod(apps, 0o755)
	if _, err := Move(src, apps); err != ErrNoPermission {
		t.Fatalf("want ErrNoPermission, got %v", err)
	}
}
