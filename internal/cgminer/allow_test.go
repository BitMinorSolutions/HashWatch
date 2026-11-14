package cgminer

import (
	"fmt"
	"strings"
	"testing"
)

func TestOnlyDocumentedWrites(t *testing.T) {
	ok := []string{"0,workmode,set,0", "0,workmode,set,1", "0,workmode,set,2", "0,ledset,1-100-50-255-106-0", "0,ledset,0-0-0-0-0-0"}
	bad := []string{"0,workmode,set,3", "0,workmode,set,-1", "0,workmode,set,2;reboot", "0,reboot,0", "0,fan-spd,100", "0,ledset,5-100-50-1-2-3",
		"0,ledset,1-101-50-1-2-3", "0,ledset,1-100-50-256-0-0", "1,workmode,set,2", "0,voltage,set,1300", "0,freq,700", ""}
	for _, p := range ok {
		if !AllowedWrite(p) {
			t.Errorf("should allow %q", p)
		}
	}
	for _, p := range bad {
		if AllowedWrite(p) {
			t.Errorf("must refuse %q", p)
		}
		if _, err := writeCmd("127.0.0.1:1", "ascset", p, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ascset sent %q (%v)", p, err)
		}
	}
	if _, err := SetWorkMode("127.0.0.1:1", 3); err == nil {
		t.Error("work level 3 accepted")
	}
}

func TestSwitchPoolAllowedOnly(t *testing.T) {
	for i := 0; i <= 15; i++ {
		if !AllowedCommand("switchpool", fmt.Sprintf("%d", i)) {
			t.Errorf("should allow switchpool|%d", i)
		}
	}
	badParam := []string{"16", "-1", "01", "00", "1e1", "1 ", " 1", "1;reboot", "1,addpool", "", "abc"}
	for _, p := range badParam {
		if AllowedCommand("switchpool", p) {
			t.Errorf("must refuse switchpool|%q", p)
		}
		if _, err := writeCmd("127.0.0.1:1", "switchpool", p, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("switchpool sent %q (%v)", p, err)
		}
	}
	for _, cmd := range []string{"addpool", "removepool", "enablepool", "disablepool", "poolpriority", "poolquota", "restart", "reboot", "kill"} {
		if AllowedCommand(cmd, "0") || AllowedCommand(cmd, "") {
			t.Errorf("must refuse %s", cmd)
		}
		if _, err := writeCmd("127.0.0.1:1", cmd, "0", 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("sent %s (%v)", cmd, err)
		}
	}
	if _, err := SwitchPool("127.0.0.1:1", 16); err == nil {
		t.Error("pool 16 accepted")
	}
	if _, err := SwitchPool("127.0.0.1:1", -1); err == nil {
		t.Error("pool -1 accepted")
	}
}
