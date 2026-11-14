package web

import (
	"os"
	"strings"
	"testing"
)

// newAlerts used to treat a last-seen id of 0 as "not ready", so a fresh
// client skipped the first live alert (including a block win) until reload.
func TestNewAlertsZeroBaselineIsReady(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if strings.Contains(js, "if (!S.alertSeen) return;") {
		t.Fatal("newAlerts still treats a 0 baseline as unset, so the first alert on a fresh install is skipped")
	}
	if !strings.Contains(js, "if (S.alertSeen == null) return;") {
		t.Fatal("newAlerts should wait only until a baseline is set; 0 (empty history) must be valid")
	}
	if !strings.Contains(js, "alertSeen: null") {
		t.Fatal("S.alertSeen must start unset so the first load can baseline even when no alerts exist yet")
	}
}
