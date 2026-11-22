package cgminer

import (
	"strings"
	"testing"

	"github.com/hashwatch/hashwatch/internal/auto"
	"github.com/hashwatch/hashwatch/internal/model"
)

func fp(v float64) *float64 { return &v }

func warnSnap(avg, target, max, dh float64) *model.Snapshot {
	s := &model.Snapshot{OK: true}
	s.Temps.Avg, s.Temps.Target, s.Temps.Max = fp(avg), fp(target), fp(max)
	s.Shares.DHPct = fp(dh)
	return s
}

func has(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestWarningsThresholds(t *testing.T) {
	// Live 3S capture: TAvg 84, TarT 85, TMax 92, DH 2.542% — no warnings.
	if w := Warnings(warnSnap(84, 85, 92, 2.542)); len(w) != 0 {
		t.Errorf("live 3S values: got %v, want none", w)
	}
	// Nano 3 fixture: TAvg 90, TarT 90, TMax 92, DH 1.827% — stays green.
	if w := Warnings(warnSnap(90, 90, 92, 1.827)); len(w) != 0 {
		t.Errorf("Nano 3 fixture values: got %v, want none", w)
	}
	// Normal regulation just under or at the 2°C allowance does not fire.
	for _, c := range []struct{ avg, target float64 }{
		{85, 85}, {86, 85}, {87, 85}, {90, 90}, {92, 90},
	} {
		if w := Warnings(warnSnap(c.avg, c.target, 92, 2.5)); has(w, "Average chip") {
			t.Errorf("avg %.0f vs target %.0f should not warn, got %v", c.avg, c.target, w)
		}
	}
	// TAvg well above TarT warns (yellow).
	w := Warnings(warnSnap(90, 85, 92, 2.5))
	if !has(w, "Average chip temperature 90°C is above the 85°C target.") {
		t.Errorf("TAvg well above TarT: got %v", w)
	}
	if has(w, "Hottest chip") || has(w, "error rate") {
		t.Errorf("only the average warning should fire, got %v", w)
	}
	// TMax 95 or more raises the safety alarm; 94 does not.
	if w := Warnings(warnSnap(84, 85, 94, 2.5)); has(w, "Hottest chip") {
		t.Errorf("TMax 94 should not raise the safety alarm, got %v", w)
	}
	w = Warnings(warnSnap(84, 85, 95, 2.5))
	if !has(w, "Hottest chip is 95°C (safety limit is 95°C).") {
		t.Errorf("TMax 95: got %v", w)
	}
	w = Warnings(warnSnap(84, 85, 98, 2.5))
	if !has(w, "Hottest chip is 98°C (safety limit is 95°C).") {
		t.Errorf("TMax 98: got %v", w)
	}
	// DH 5% or more warns; just under does not.
	if w := Warnings(warnSnap(84, 85, 92, 4.999)); has(w, "error rate") {
		t.Errorf("DH 4.999%% should not warn, got %v", w)
	}
	w = Warnings(warnSnap(84, 85, 92, 5))
	if !has(w, "Chip error rate (DH) is 5.000%.") {
		t.Errorf("DH 5%%: got %v", w)
	}
	w = Warnings(warnSnap(84, 85, 92, 6.25))
	if !has(w, "Chip error rate (DH) is 6.250%.") {
		t.Errorf("DH 6.25%%: got %v", w)
	}
	if auto.AvgOverTargetC != 2 || auto.ChipSafetyC != 95 || auto.DHWarnPct != 5 {
		t.Fatalf("unexpected published defaults: over=%.0f safety=%.0f dh=%.0f", auto.AvgOverTargetC, auto.ChipSafetyC, auto.DHWarnPct)
	}
}
