package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	pendingName     = "update-pending.json"
	unconfirmedName = "update-unconfirmed"
	lastName        = "update-last.json"
	skipName        = "update-skip"
)

// Pending records an in-flight binary swap, so a failed start can roll back.
type Pending struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	SHA256  string    `json:"sha256"`
	Started time.Time `json:"started"`
	Exe     string    `json:"exe"`
}

// Last holds the most recent update outcome (for Settings and to skip a bad version).
type Last struct {
	At      time.Time `json:"at"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	OK      bool      `json:"ok"`
	Rolled  bool      `json:"rolled_back,omitempty"`
	Error   string    `json:"error,omitempty"`
	Version string    `json:"version,omitempty"`
}

func pendingPath(dir string) string     { return filepath.Join(dir, pendingName) }
func unconfirmedPath(dir string) string { return filepath.Join(dir, unconfirmedName) }
func lastPath(dir string) string        { return filepath.Join(dir, lastName) }
func skipPath(dir string) string        { return filepath.Join(dir, skipName) }

func backupPath(exe string) string  { return exe + ".bak" }
func stagingPath(exe string) string { return exe + ".new" }
func badPath(exe string) string     { return exe + ".bad" }

// InstallBinary writes body to exe atomically, keeping the previous file as exe.bak.
// It also writes a pending record and an unconfirmed marker in dir.
func InstallBinary(exe, dir, from, to string, body []byte) (*Pending, error) {
	if exe == "" {
		return nil, fmt.Errorf("install path is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	hexSum := hex.EncodeToString(sum[:])
	stage := stagingPath(exe)
	if err := os.WriteFile(stage, body, 0o755); err != nil {
		return nil, err
	}
	if f, err := os.Open(stage); err == nil {
		_ = f.Sync()
		f.Close()
	}
	if st, err := os.Stat(stage); err == nil && !st.Mode().IsRegular() {
		os.Remove(stage)
		return nil, fmt.Errorf("staged update is not a regular file")
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		os.Remove(stage)
		return nil, err
	}
	// Keep a backup of the running binary, if any.
	if _, err := os.Stat(exe); err == nil {
		// Same filesystem rename is atomic; copy if that fails (cross-device).
		if err := os.Rename(exe, backupPath(exe)); err != nil {
			if cpErr := copyFile(exe, backupPath(exe)); cpErr != nil {
				os.Remove(stage)
				return nil, fmt.Errorf("backup current binary: %w", err)
			}
			os.Remove(exe)
		}
	}
	if err := os.Rename(stage, exe); err != nil {
		_ = os.Rename(backupPath(exe), exe)
		os.Remove(stage)
		return nil, fmt.Errorf("activate new binary: %w", err)
	}
	p := &Pending{From: from, To: to, SHA256: hexSum, Started: time.Now().UTC(), Exe: exe}
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(pendingPath(dir), b, 0o600); err != nil {
		return p, err
	}
	// The supervise script (not this process) sets update-unconfirmed when it
	// actually launches the new binary. Writing it here would make the next
	// start roll back before the new version has a chance to come up.
	return p, nil
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, in, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func ReadPending(dir string) (*Pending, error) {
	b, err := os.ReadFile(pendingPath(dir))
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func ReadLast(dir string) *Last {
	b, err := os.ReadFile(lastPath(dir))
	if err != nil {
		return nil
	}
	var l Last
	if json.Unmarshal(b, &l) != nil {
		return nil
	}
	return &l
}

func writeLast(dir string, l Last) {
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	_ = os.WriteFile(lastPath(dir), b, 0o600)
}

func clearPending(dir string) {
	os.Remove(pendingPath(dir))
	os.Remove(unconfirmedPath(dir))
}

// Unconfirmed reports whether a previous start never confirmed a pending update.
func Unconfirmed(dir string) bool {
	_, err := os.Stat(unconfirmedPath(dir))
	return err == nil
}

// SkipVersion remembers a version that rolled back, so auto-update will not retry it.
func SkipVersion(dir, version string) error {
	return os.WriteFile(skipPath(dir), []byte(version+"\n"), 0o600)
}

func SkippedVersion(dir string) string {
	b, err := os.ReadFile(skipPath(dir))
	if err != nil {
		return ""
	}
	return string(trimNL(b))
}

func ClearSkip(dir string) { os.Remove(skipPath(dir)) }

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

// Rollback restores exe.bak over exe and clears the pending marker.
func Rollback(exe, dir string) error {
	bak := backupPath(exe)
	if _, err := os.Stat(bak); err != nil {
		return fmt.Errorf("no backup to roll back to: %w", err)
	}
	_ = os.Rename(exe, badPath(exe))
	if err := os.Rename(bak, exe); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	_ = os.Chmod(exe, 0o755)
	return nil
}

// Confirm marks a pending update as healthy (version matches) or rolls it back.
// pingOK means this process is already serving /api/ping as HashWatch.
func Confirm(dir, exe, runningVersion string, pingOK bool) (rolled bool, err error) {
	p, err := ReadPending(dir)
	if err != nil {
		if os.IsNotExist(err) {
			os.Remove(unconfirmedPath(dir))
			return false, nil
		}
		return false, err
	}
	if exe == "" {
		exe = p.Exe
	}
	if pingOK && runningVersion == p.To {
		clearPending(dir)
		writeLast(dir, Last{OK: true, From: p.From, To: p.To, Version: runningVersion})
		return false, nil
	}
	if err := Rollback(exe, dir); err != nil {
		writeLast(dir, Last{OK: false, Rolled: false, From: p.From, To: p.To, Error: err.Error(), Version: runningVersion})
		return false, err
	}
	_ = SkipVersion(dir, p.To)
	clearPending(dir)
	writeLast(dir, Last{OK: false, Rolled: true, From: p.From, To: p.To, Error: "new version failed its health check; rolled back to " + p.From, Version: runningVersion})
	return true, nil
}

// RollbackIfUnconfirmed is used by the supervise script / a fresh start: if the
// last process never confirmed, restore the backup before launching.
func RollbackIfUnconfirmed(exe, dir string) (bool, error) {
	if !Unconfirmed(dir) {
		return false, nil
	}
	p, _ := ReadPending(dir)
	to, from := "", ""
	if p != nil {
		to, from = p.To, p.From
		if exe == "" {
			exe = p.Exe
		}
	}
	if err := Rollback(exe, dir); err != nil {
		return false, err
	}
	if to != "" {
		_ = SkipVersion(dir, to)
	}
	clearPending(dir)
	writeLast(dir, Last{OK: false, Rolled: true, From: from, To: to, Error: "new version did not become healthy; rolled back"})
	return true, nil
}
