package cgminer

import (
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/sim"
)

func TestParams(t *testing.T) {
	p, _ := WorkModeParam(1)
	if p != "0,workmode,set,1" {
		t.Fatal(p)
	}
	p, _ = LEDParam(3, 82, 50, "#FF6A00")
	if p != "0,ledset,3-82-50-255-106-0" {
		t.Fatal(p)
	}
	for _, bad := range [][]any{{5, 50, 50, "#ffffff"}, {1, 101, 50, "#ffffff"}, {1, 50, 50, "red"}, {1, 50, 50, "#fff,0,reboot"}} {
		if _, err := LEDParam(bad[0].(int), bad[1].(int), bad[2].(int), bad[3].(string)); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := WorkModeParam(3); err == nil {
		t.Error("accepted level 3")
	}
}

func TestControlAgainstSimulator(t *testing.T) {
	for _, reportLED := range []bool{false, true} {
		m := sim.New()
		m.ReportLED = reportLED
		ln, err := m.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		res, err := SetWorkMode(addr, 0)
		if err != nil || !res.OK || res.Sent != "ascset|0,workmode,set,0" {
			t.Fatalf("workmode: %+v %v", res, err)
		}
		res, err = SetLED(addr, 1, 40, 50, "#00ffcc")
		if err != nil || !res.OK || !strings.Contains(res.Message, "led set ok") {
			t.Fatalf("led: %+v %v", res, err)
		}
		s := Fetch(addr, 2*time.Second)
		if s.Control == nil || s.Control.WorkName != "low" || s.Control.LEDMode != "fixed" {
			t.Fatalf("readback %+v", s.Control)
		}
		if reportLED && (s.Control.LEDHex != "#00ffcc" || *s.Control.LEDBright != 40) {
			t.Fatalf("LEDUser readback %+v", s.Control)
		}
		got := m.Log()
		want := []string{"ascset|0,workmode,set,0", "ascset|0,ledset,1-40-50-0-255-204"}
		if got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("sent %v", got)
		}
		for _, c := range got[2:] {
			if strings.HasPrefix(c, "ascset") {
				t.Fatalf("Fetch sent a write: %s", c)
			}
		}
		ln.Close()
	}
}
