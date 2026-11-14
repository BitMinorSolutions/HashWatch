package cgminer

import (
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
		if _, err := ascset("127.0.0.1:1", p, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ascset sent %q (%v)", p, err)
		}
	}
	if _, err := SetWorkMode("127.0.0.1:1", 3); err == nil {
		t.Error("work level 3 accepted")
	}
}
