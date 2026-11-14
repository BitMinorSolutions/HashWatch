// Package install implements "Move Hashwatch to Applications?" for the macOS app:
// detect a bundle running from Downloads, a disk image or App Translocation, copy it
// into /Applications with ditto (keeps permissions, xattrs and the code signature),
// and relaunch from there.
package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Info is shown to the UI.
type Info struct {
	Offer bool   `json:"offer"`
	Name  string `json:"name"`  // e.g. "Hashwatch"
	Where string `json:"where"` // "downloads", "disk image", "translocated", "elsewhere"
	From  string `json:"from"`
	Dest  string `json:"dest"`
}

// ErrNoPermission means the Applications folder isn't writable for this user.
var ErrNoPermission = errors.New("no permission to write to the Applications folder")

// Bundle returns the .app path that contains exe, or "".
func Bundle(exe string) string {
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	return exe[:i+len(".app")]
}

// AppsDir is the system Applications folder (overridable for tests).
func AppsDir() string {
	if d := os.Getenv("HASHWATCH_APPLICATIONS_DIR"); d != "" {
		return d
	}
	return "/Applications"
}

// Check decides whether to offer the move for the bundle that contains exe.
func Check(exe, home string) Info {
	b := Bundle(exe)
	if b == "" {
		return Info{}
	}
	name := strings.TrimSuffix(filepath.Base(b), ".app")
	in := Info{Name: name, From: b, Dest: filepath.Join(AppsDir(), filepath.Base(b))}
	parent := filepath.Dir(b)
	if parent == filepath.Clean(AppsDir()) || (home != "" && parent == filepath.Join(home, "Applications")) {
		return in
	}
	l := strings.ToLower(b)
	switch {
	case strings.Contains(l, "/apptranslocation/"):
		in.Where = "translocated"
	case strings.HasPrefix(l, "/volumes/"):
		in.Where = "disk image"
	case strings.Contains(l, "/downloads/"):
		in.Where = "downloads"
	default:
		in.Where = "elsewhere"
	}
	in.Offer = true
	return in
}

// Copy copies a bundle; ditto on macOS (keeps modes, symlinks, xattrs, signature).
var Copy = func(src, dst string) error {
	cmd := exec.Command("/usr/bin/ditto", src, dst)
	if runtime.GOOS != "darwin" {
		cmd = exec.Command("cp", "-a", src, dst) // tests / non-Mac only
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "permission denied") || strings.Contains(msg, "Operation not permitted") {
			return ErrNoPermission
		}
		return fmt.Errorf("%v: %s", err, msg)
	}
	return nil
}

// ClearQuarantine removes the download quarantine flag from the installed copy, so
// macOS doesn't translocate it again (the user already chose Open Anyway and Move).
var ClearQuarantine = func(path string) {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", path).Run()
	}
}

// Move installs src into dir (replacing an older copy there) and returns the new path.
func Move(src, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", classify(err)
	}
	probe, err := os.CreateTemp(dir, ".hashwatch-write-test-*")
	if err != nil {
		return "", classify(err)
	}
	probe.Close()
	os.Remove(probe.Name())

	name := filepath.Base(src)
	dst := filepath.Join(dir, name)
	tmp := filepath.Join(dir, "."+name+".installing")
	os.RemoveAll(tmp)
	if err := Copy(src, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if _, err := os.Lstat(dst); err == nil {
		old := filepath.Join(dir, fmt.Sprintf(".%s.old-%d", name, time.Now().Unix()))
		if err := os.Rename(dst, old); err != nil {
			os.RemoveAll(tmp)
			return "", classify(err)
		}
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Rename(old, dst)
			return "", classify(err)
		}
		os.RemoveAll(old)
	} else if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return "", classify(err)
	}
	ClearQuarantine(dst)
	return dst, nil
}

func classify(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return ErrNoPermission
	}
	return err
}
