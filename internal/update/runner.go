package update

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Interval is how often a service copy checks the manifest (about 5 minutes).
const Interval = 5 * time.Minute

// Runner checks a signed manifest and, in service mode, applies updates.
// It never talks to a miner or to anything except the configured release URL
// (and the binary URLs listed in a verified manifest).
type Runner struct {
	Version  string
	Exe      string
	Dir      string
	URL      func() string
	Channel  func() string
	Service  bool
	Interval time.Duration
	Client   *http.Client
	// Restart replaces this process. Default: HASHWATCH_RESTART_CMD if set,
	// otherwise exit this process (see Exit) so the supervisor brings up the
	// new binary. On the Pi that is systemd (Restart=always) running the
	// supervise script, which also health-checks and rolls back. The service
	// runs as an unprivileged user, so it must never call systemctl itself:
	// polkit refuses ("Interactive authentication required").
	Restart func() error
	// Exit ends this process cleanly after an update or a rollback
	// (default os.Exit(0)). main wires the graceful shutdown here.
	Exit func()
	// ExitDelay lets the HTTP reply for "Update now" reach the browser before
	// the process exits (default 750ms).
	ExitDelay time.Duration
	// PublicKey overrides the baked-in key; tests set this. Production uses PublicKey.
	Key ed25519.PublicKey
	Now func() time.Time

	mu      sync.Mutex
	lastErr string
	checked time.Time
	latest  string
	notes   string
	avail   bool
	busy    bool
}

// Status is a snapshot for Settings / /api/update.
type Status struct {
	Enabled    bool   `json:"enabled"`
	Service    bool   `json:"service"`
	Channel    string `json:"channel"`
	Current    string `json:"current"`
	Latest     string `json:"latest,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Available  bool   `json:"available"`
	Checking   bool   `json:"checking,omitempty"`
	LastCheck  string `json:"last_check,omitempty"`
	Error      string `json:"error,omitempty"`
	Last       *Last  `json:"last,omitempty"`
	CanApply   bool   `json:"can_apply"`
	URLSet     bool   `json:"url_set"`
	HasKey     bool   `json:"has_key"`
	RolledBack bool   `json:"rolled_back,omitempty"`
}

func (r *Runner) key() (ed25519.PublicKey, error) {
	if len(r.Key) == ed25519.PublicKeySize {
		return r.Key, nil
	}
	return ParsePublicKey(PublicKey)
}

func (r *Runner) url() string {
	if r != nil && r.URL != nil {
		return strings.TrimSpace(r.URL())
	}
	return ""
}

func (r *Runner) channel() string {
	if r != nil && r.Channel != nil {
		return NormalizeChannel(r.Channel())
	}
	return "stable"
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return defaultClient()
}

func (r *Runner) restart() error {
	if r.Restart != nil {
		return r.Restart()
	}
	if cmd := strings.TrimSpace(os.Getenv("HASHWATCH_RESTART_CMD")); cmd != "" {
		c := exec.Command("/bin/sh", "-c", cmd)
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("restart: %w (%s)", err, bytesPreview(out))
		}
		return nil
	}
	r.exitSoon()
	return nil
}

// exitSoon ends the process shortly, so the supervisor (systemd Restart=always
// + supervise) starts the binary now on disk. Needs no privileges.
func (r *Runner) exitSoon() {
	d := r.ExitDelay
	if d <= 0 {
		d = 750 * time.Millisecond
	}
	exit := r.Exit
	if exit == nil {
		exit = func() { os.Exit(0) }
	}
	log.Printf("update: exiting so the service manager starts the new binary")
	go func() {
		time.Sleep(d)
		exit()
	}()
}

func bytesPreview(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

// Snapshot returns the current updater status. It does not touch the network.
func (r *Runner) Snapshot() Status {
	if r == nil {
		return Status{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, keyErr := r.key()
	st := Status{
		Enabled:   r.url() != "" && keyErr == nil,
		Service:   r.Service,
		Channel:   r.channel(),
		Current:   r.Version,
		Latest:    r.latest,
		Notes:     r.notes,
		Available: r.avail,
		Checking:  r.busy,
		Error:     r.lastErr,
		Last:      ReadLast(r.Dir),
		CanApply:  r.Service && r.url() != "" && keyErr == nil,
		URLSet:    r.url() != "",
		HasKey:    keyErr == nil,
	}
	if !r.checked.IsZero() {
		st.LastCheck = r.checked.Format(time.RFC3339)
	}
	if st.Last != nil && st.Last.Rolled {
		st.RolledBack = true
	}
	if keyErr != nil && r.url() != "" {
		if st.Error == "" {
			st.Error = keyErr.Error()
		}
	}
	return st
}

// Loop checks the manifest on interval until ctx is done. Failed checks are
// logged and retried; the miner is never contacted.
func (r *Runner) Loop(ctx context.Context) {
	if r == nil {
		return
	}
	wait := r.Interval
	if wait <= 0 {
		wait = Interval
	}
	// First look shortly after start, then every wait.
	t := time.NewTimer(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := r.Check(ctx, r.Service); err != nil {
				log.Printf("update: %v", err)
			}
			t.Reset(wait)
		}
	}
}

// Check fetches and verifies the manifest. If apply is true and a newer build
// is available, it installs the binary and restarts. Auto-apply skips a
// version that already failed a health check, unless the user asked (apply
// from "Update now" uses Check(ctx, true) after ClearSkip, see ApplyNow).
func (r *Runner) Check(ctx context.Context, apply bool) (Status, error) {
	if r == nil {
		return Status{}, fmt.Errorf("updater is not configured")
	}
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return r.Snapshot(), fmt.Errorf("an update is already in progress")
	}
	r.busy = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.busy = false; r.mu.Unlock() }()

	st, err := r.check(ctx, apply)
	r.mu.Lock()
	r.checked = r.now()
	if err != nil {
		r.lastErr = err.Error()
	} else {
		r.lastErr = ""
	}
	r.mu.Unlock()
	return st, err
}

func (r *Runner) check(ctx context.Context, apply bool) (Status, error) {
	url := r.url()
	if url == "" {
		return r.Snapshot(), fmt.Errorf("no update URL is configured")
	}
	pub, err := r.key()
	if err != nil {
		return r.Snapshot(), err
	}
	raw, err := FetchEnvelope(ctx, r.client(), url)
	if err != nil {
		return r.Snapshot(), err
	}
	man, _, err := OpenEnvelope(raw, pub)
	if err != nil {
		return r.Snapshot(), err
	}
	rel, err := man.Release(r.channel())
	if err != nil {
		return r.Snapshot(), err
	}
	plat := Platform(runtime.GOOS, runtime.GOARCH)
	build, err := rel.Build(plat)
	if err != nil {
		return r.Snapshot(), err
	}

	newer := Newer(r.Version, rel.Version)
	r.mu.Lock()
	r.latest = rel.Version
	r.notes = rel.Notes
	r.avail = newer && rel.Version != r.Version
	r.mu.Unlock()

	if !apply || !r.avail {
		return r.Snapshot(), nil
	}
	if !r.Service {
		return r.Snapshot(), fmt.Errorf("automatic updates only run on the HashWatch service (the Pi)")
	}
	if skip := SkippedVersion(r.Dir); skip != "" && skip == rel.Version {
		r.mu.Lock()
		r.lastErr = "version " + rel.Version + " failed a health check earlier; skipped until you press Update now"
		r.mu.Unlock()
		return r.Snapshot(), nil
	}
	if r.Exe == "" {
		return r.Snapshot(), fmt.Errorf("no executable path to replace")
	}
	body, err := FetchBinary(ctx, r.client(), build)
	if err != nil {
		return r.Snapshot(), err
	}
	if _, err := InstallBinary(r.Exe, r.Dir, r.Version, rel.Version, body); err != nil {
		return r.Snapshot(), err
	}
	log.Printf("update: installed %s (%s), restarting", rel.Version, plat)
	if err := r.restart(); err != nil {
		// Swap happened; try to roll back so we don't leave a pending unstarted binary.
		if rbErr := Rollback(r.Exe, r.Dir); rbErr != nil {
			return r.Snapshot(), fmt.Errorf("restart failed (%v) and rollback failed (%v)", err, rbErr)
		}
		clearPending(r.Dir)
		return r.Snapshot(), fmt.Errorf("restart failed, rolled back: %w", err)
	}
	return r.Snapshot(), nil
}

// ApplyNow clears a skipped version and applies a pending update if one exists.
func (r *Runner) ApplyNow(ctx context.Context) (Status, error) {
	if r != nil {
		ClearSkip(r.Dir)
	}
	return r.Check(ctx, true)
}

// RestartNow triggers the configured restart hook (process exit by default).
func (r *Runner) RestartNow() error {
	if r == nil {
		return nil
	}
	return r.restart()
}
