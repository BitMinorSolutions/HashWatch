package cgminer

import (
	"fmt"
	"strings"
	"testing"
)

func TestOnlyDocumentedWrites(t *testing.T) {
	ok := []string{"0,workmode,set,0", "0,workmode,set,1", "0,workmode,set,2", "0,ledset,1-100-50-255-106-0", "0,ledset,0-0-0-0-0-0"}
	bad := []string{"0,workmode,set,3", "0,workmode,set,-1", "0,workmode,set,2;reboot", "0,reboot,0", "0,ledset,5-100-50-1-2-3",
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

func TestFanSpdAllowList(t *testing.T) {
	ok := []string{"0,fan-spd,-1", "0,fan-spd,40", "0,fan-spd,50", "0,fan-spd,99", "0,fan-spd,100"}
	for _, p := range ok {
		if !AllowedWrite(p) || !AllowedCommand("ascset", p) {
			t.Errorf("should allow %q", p)
		}
		if _, err := ParseFanSpdParam(p); err != nil {
			t.Errorf("ParseFanSpdParam(%q): %v", p, err)
		}
	}
	if p, err := FanSpdParam(-1); err != nil || p != "0,fan-spd,-1" {
		t.Errorf("FanSpdParam(-1) = %q %v", p, err)
	}
	if p, err := FanSpdParam(50); err != nil || p != "0,fan-spd,50" {
		t.Errorf("FanSpdParam(50) = %q %v", p, err)
	}
	if p, err := FanSpdParam(40); err != nil || p != "0,fan-spd,40" {
		t.Errorf("FanSpdParam(40) = %q %v", p, err)
	}
	if p, err := FanSpdParam(100); err != nil || p != "0,fan-spd,100" {
		t.Errorf("FanSpdParam(100) = %q %v", p, err)
	}

	bad := []string{
		"0,fan-spd,39", "0,fan-spd,101", "0,fan-spd,15", "0,fan-spd,0",
		"0,fan-spd,1", "0,fan-spd,14", "0,fan-spd,200", "0,fan-spd,-2",
		"0,fan-spd,040", "0,fan-spd,+50", "0,fan-spd,50 ", "0,fan-spd, 50",
		"0,fan-spd,50.0", "0,fan-spd,50e0", "0,fan-spd,0x50", "0,fan-spd,",
		"0,fan-spd", "0,fan-spd,auto", "0,fan-spd,fifty", "0,fan-spd,--1",
		"0,fan-spd,-01", "0,fan-spd,-1 ", "0,fan-spd, -1", "fan-spd,50",
		"0,fan-spd,50;reboot", "0,fan-spd,50,0,workmode,set,2",
		"0,fan-spd,50|ascset", "0,fan-spd,-1,0", "0,fan-spd,50\n",
		"0,fan-spd,50\r", "0,fan-spd,50\t", "1,fan-spd,50",
		"0,fan-spd,50 ", "0,FAN-SPD,50",
	}
	for _, p := range bad {
		if AllowedWrite(p) || AllowedCommand("ascset", p) {
			t.Errorf("must refuse %q", p)
		}
		if _, err := writeCmd("127.0.0.1:1", "ascset", p, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ascset sent %q (%v)", p, err)
		}
	}
	for _, n := range []int{39, 101, 15, 0, 1, 14, 200, -2, 101} {
		if _, err := FanSpdParam(n); err == nil {
			t.Errorf("FanSpdParam(%d) accepted", n)
		}
		if _, err := SetFanSpd("127.0.0.1:1", n); err == nil {
			t.Errorf("SetFanSpd(%d) accepted", n)
		}
	}
}
