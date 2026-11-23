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
	js := appJS(t)
	if !strings.Contains(js, "function modelName(") {
		t.Fatal("shared modelName() is missing")
	}
	if !strings.Contains(js, "function minerTitle()") {
		t.Fatal("header should use minerTitle() so a stored 'Avalon Nano3s Nano3s' name is not shown doubled")
	}
	if !strings.Contains(js, `id="minerTitle"`) {
		t.Fatal("header miner name needs id=minerTitle")
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

func appCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMiningViewAndHeroLabel(t *testing.T) {
	js := appJS(t)
	css := appCSS(t)
	for _, s := range []string{
		"Live (chip-reported)",
		`id="secMining"`,
		`id="kpiRow"`,
		`id="mineGrid"`,
		`id="dashAlert"`,
		"New block ",
		"of the way to a block",
		"like flipping heads ",
		"Hidden while the miner is offline",
		"prefers-reduced-motion",
	} {
		if !strings.Contains(js, s) && !strings.Contains(css, s) {
			t.Errorf("Mining / KPI UI missing %q", s)
		}
	}
	if !strings.Contains(js, "Live <b class=\"num\">${fmt(hv5, 3)}</b> TH/s") {
		t.Fatal("chart header should lead with the live chip-reported hashrate")
	}
	if !strings.Contains(js, "5m avg") {
		t.Fatal("chart header should still show the 5-minute average as secondary")
	}
	if strings.Contains(js, "5-minute average") {
		t.Fatal("chart header must not lead with the 5-minute average")
	}
	if !strings.Contains(js, `{ k: "hhero", label: "Live (chip-reported)"`) {
		t.Fatal("hashrate chart main series must be live chip-reported")
	}
	if strings.Contains(js, `{ k: "h15m"`) || strings.Contains(js, `"15m avg"`) {
		t.Fatal("15m avg must not appear on the hashrate chart")
	}
	if !strings.Contains(js, "MINING.update()") {
		t.Fatal("lottery load must refresh the Mining card so odds stay in sync")
	}
	for _, gone := range []string{
		`id="hashAnim"`,
		`id="mineCv"`,
		`id="mineStory"`,
		`id="mineMeter"`,
		`id="mineFeed"`,
		"How close have we gotten?",
		"Your miner is guessing about ",
		"Illustration — not individual hashes",
		"Share hits",
		"timeWindow(",
		"bucketHits(",
		"shareAxis(",
		"startAnim(",
		`fillText("×`,
	} {
		if strings.Contains(js, gone) || strings.Contains(css, gone) {
			t.Errorf("removed Mining chart/animation still present: %q", gone)
		}
	}
	if strings.Contains(js, "for (let i = 0; i < hits.length; i++) if (hits[i].diff > b) b = hits[i].diff") {
		t.Fatal("best share must not be raised by last-share hit difficulties")
	}
}

func TestOfflineHidesHashrateUnit(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, "unit.hidden = !unitShow") && !strings.Contains(js, "unit.hidden = !d.ok") {
		t.Fatal("KPI units must be hidden when the miner is offline")
	}
	if !strings.Contains(js, "Miner offline") || !strings.Contains(js, `id="dashOffline"`) {
		t.Fatal("offline state needs a dedicated screen")
	}
}

func TestLotteryOddsFollowLiveHeadlineAndHideWhenOffline(t *testing.T) {
	js := appJS(t)
	if !strings.Contains(js, "const minerOn = !(S.data && S.data.ok === false) && o.hashrate_hs > 0;") {
		t.Fatal("lottery chance/time must hide when the miner is offline")
	}
	if !strings.Contains(js, "one_in_per_day") || !strings.Contains(js, "like flipping heads") {
		t.Fatal("Mining odds sentence must use the lottery card's one_in_per_day")
	}
}

func TestNoNewLookbehindInAppJS(t *testing.T) {
	js := appJS(t)
	// notices() used to split on a lookbehind, which Safari older than 16.4 rejects.
	n := strings.Count(js, "?<=") + strings.Count(js, "?<!")
	if n != 0 {
		t.Fatalf("app.js has %d regex lookbehinds; want none (notices() must use indexOf)", n)
	}
	if !strings.Contains(js, `x.indexOf(". ")`) {
		t.Fatal("notices() should take the first sentence with indexOf, not a lookbehind")
	}
	for _, s := range []string{"toSorted(", "toReversed(", "structuredClone(", ".findLast(", "Object.hasOwn("} {
		if strings.Contains(js, s) {
			t.Errorf("app.js uses %s, which needs a newer Safari than we support", s)
		}
	}
}
