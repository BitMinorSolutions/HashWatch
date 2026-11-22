package web

import (
	"os"
	"strings"
	"testing"
)

func appJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// v0.9.0 accuracy audit against a live Nano 3S (Device Hardware% 0.0000, DH[2.542%]):
// the "Hardware errors" row paired cgminer's count with the MM's DH rate, so it read
// "0 · 2.542%". The count now goes with cgminer's own Device Hardware%, and DH has its own row.
func TestHardwareErrorRowUsesMatchingPercent(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, `["Hardware errors", `+"`${fmt(sh.hw_errors)} <span class=\"t3\">· ${isNum(sh.device_hw_pct)") {
		t.Fatal("Hardware errors should show cgminer's Device Hardware% next to the count")
	}
	if !strings.Contains(js, `["Chip error rate (DH)", isNum(sh.dh_pct)`) {
		t.Fatal("DH should be its own row")
	}
}

// PROD "Avalon Nano3s" + MODEL "Nano3s" was shown as "Avalon Nano3s Nano3s".
func TestModelNotDoubled(t *testing.T) {
	if !strings.Contains(appJS(t), `id.product.toLowerCase().includes(id.model.toLowerCase()) ? id.product`) {
		t.Fatal("Model should not repeat the model name already in the product name")
	}
}

// The Device list said "Fixed" while Controls said "Steady" for the same light effect.
func TestLightNameMatchesControls(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, `esc(MODE[c.led_mode] || cap(c.led_mode))`) {
		t.Fatal("Device list should name the light effect the same way Controls does")
	}
}

// Share: tunnel visitors without a share-link session get the "shared by link only"
// page, never the sign-in form; and nothing user-facing says "remote access".
func TestShareCopy(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, "if (S.state.shared && !S.state.authed) return viewShareOnly();") {
		t.Fatal("share-only page missing from boot")
	}
	if strings.Index(js, "viewShareOnly();") > strings.Index(js, "return viewLogin();") {
		t.Fatal("the share-only check must come before the sign-in form")
	}
	low := strings.ToLower(js)
	for _, bad := range []string{"remote access", "remote link", "/api/security/viewlink"} {
		if strings.Contains(low, bad) {
			t.Errorf("app.js still contains %q", bad)
		}
	}
}

// Since v0.7.0 the sign-in form posted /api/login without the X-CSRF-Token header,
// so every password sign-in from a browser was refused with "Missing or invalid
// security token" (found in the v0.9.0 end-to-end run).
func TestLoginSendsCSRFToken(t *testing.T) {
	js := appJS(t)
	i := strings.Index(js, `fetch("/api/login"`)
	if i < 0 {
		t.Fatal("login fetch not found")
	}
	end := strings.Index(js[i:], "\n")
	if !strings.Contains(js[i:i+end], `"X-CSRF-Token": S.csrf`) {
		t.Fatal("the sign-in request must carry the CSRF token")
	}
}

// v0.9.1: the hottest-chip safety alarm is a red/critical notice, not the yellow warning.
func TestSafetyAlarmUsesCriticalNotice(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, "const safety = /hottest chip|safety limit/i.test(x);") {
		t.Fatal("safety alarm should be classified from the warning text")
	}
	if !strings.Contains(js, `const cls = safety ? "bad" : "warn";`) {
		t.Fatal("safety alarm should use the existing red notice.bad style")
	}
	if !strings.Contains(js, "One chip is at or above 95°C.") {
		t.Fatal("safety alarm wording should mention the 95°C limit")
	}
}

func TestTempToneUsesSafetyLimitForMaxChip(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, "const CHIP_SAFETY_C = 95, AVG_OVER_TARGET_C = 2;") {
		t.Fatal("front-end heat colors should use the same 95°C / 2°C defaults")
	}
	if !strings.Contains(js, `tempTone(tp.max, "max")`) {
		t.Fatal("max chip color should follow the 95°C safety limit, not the average target")
	}
}

func TestFanControlNote(t *testing.T) {
	js := appJS(t)
	const note = "Fixed speed. The miner won't speed the fan up on its own if the room gets hotter. The 95°C safety alarm still watches the hottest chip."
	if !strings.Contains(js, note) {
		t.Fatal("Fan Cooler control must show the fixed-speed safety note")
	}
	if !strings.Contains(js, `id="fanPanel"`) || !strings.Contains(js, `id="fanBack"`) || !strings.Contains(js, "Back to Auto") {
		t.Fatal("Fan control should include the panel and a one-tap Back to Auto")
	}
	if !strings.Contains(js, "/api/control/fan") {
		t.Fatal("Fan control should post to /api/control/fan")
	}
}
