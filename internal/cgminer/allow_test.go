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
		if p == "0,reboot,0" {
			if !AllowedCommand("ascset", p) {
				t.Errorf("reboot should be allowed as ascset|%s", p)
			}
			continue
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

func TestSetPoolAndRebootAllowed(t *testing.T) {
	ok, err := SetPoolParam("", "", 0, "stratum+tcp://solo.ckpool.org:3333", "bc1qworker.Nano3", "x")
	if err != nil {
		t.Fatal(err)
	}
	if ok != "admin,admin,0,stratum+tcp://solo.ckpool.org:3333,bc1qworker.Nano3,x" {
		t.Fatalf("param %q", ok)
	}
	if !AllowedCommand("setpool", ok) {
		t.Fatalf("should allow %q", ok)
	}
	if !AllowedCommand("ascset", RebootParam) {
		t.Fatal("should allow reboot")
	}
	bad := []string{
		"admin,admin,3,stratum+tcp://solo.ckpool.org:3333,w,x",
		"admin,admin,0,http://evil.example/pool,w,x",
		"admin,admin,0,stratum+tcp://host:3333,w,x,extra",
		"admin,admin,0,stratum+tcp://host:3333,we,r,ker,x",
		"admin,admin,01,stratum+tcp://solo.ckpool.org:3333,w,x",
		"admin,admin,0,stratum+tcp://host:99999,w,x",
		"admin,admin,0,stratum+tcp://host:3333,w ith space,x",
		"",
	}
	for _, p := range bad {
		if AllowedCommand("setpool", p) {
			t.Errorf("must refuse setpool|%q", p)
		}
		if _, err := writeCmd("127.0.0.1:1", "setpool", p, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("setpool sent %q (%v)", p, err)
		}
	}
	if _, err := SetPoolParam("admin", "admin", 0, "ftp://nope", "w", "x"); err == nil {
		t.Fatal("bad URL accepted")
	}
	if _, err := Reboot("127.0.0.1:1"); err == nil {
		t.Fatal("reboot to a closed port should fail at the network, not be refused")
	}
}
