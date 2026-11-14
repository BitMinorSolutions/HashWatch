package app

import (
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashwatch/hashwatch/internal/sim"
	"github.com/hashwatch/hashwatch/web"
)

func TestAdvancedControlsGuardedAndWorkOnSimulator(t *testing.T) {
	m := sim.New()
	ln, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	dir := t.TempDir()
	cfg, _ := LoadConfig(dir)
	cfg.Miners = []Miner{{ID: "n3", Name: "Nano 3", Host: h, Port: port, Kind: "cgminer"}}
	cfg.SetPassword("adminpass1")
	poll := NewPoller(t.TempDir(), time.Hour)
	poll.Start(cfg.Miners[0])
	defer poll.Stop("n3")
	s := &Server{Cfg: cfg, Poll: poll, Static: web.FS, InstanceID: NewID()}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	admin := &tclient{t: t, ts: ts}
	if r, _ := admin.do("POST", "/api/login", `{"password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatal("login")
	}

	body := `{"miner":"n3","pool":1,"url":"stratum+tcp://public-pool.io:21496","worker":"new.worker","worker_pass":"secret","password":"adminpass1"}`
	if r, out := admin.do("POST", "/api/control/pool", body); r.StatusCode != 403 || !strings.Contains(fmtErr(out), "Advanced") {
		t.Fatalf("off: %d %v", r.StatusCode, out)
	}
	if r, out := admin.do("POST", "/api/control/reboot", `{"miner":"n3","password":"adminpass1"}`); r.StatusCode != 403 || !strings.Contains(fmtErr(out), "Advanced") {
		t.Fatalf("reboot off: %d %v", r.StatusCode, out)
	}

	if r, out := admin.do("POST", "/api/prefs", `{"advanced_controls":true}`); r.StatusCode != 200 || out["advanced_controls"] != true {
		t.Fatalf("enable: %d %v", r.StatusCode, out)
	}

	if r, _ := admin.do("POST", "/api/security/view", `{"password":"lookonly1"}`); r.StatusCode != 200 {
		t.Fatal("set view password")
	}
	if r, out := admin.do("POST", "/api/control/pool", `{"miner":"n3","pool":1,"url":"stratum+tcp://public-pool.io:21496","worker":"new.worker","worker_pass":"secret","password":"lookonly1"}`); r.StatusCode != 403 || !strings.Contains(fmtErr(out), "admin password") {
		t.Fatalf("view password: %d %v", r.StatusCode, out)
	}
	if r, out := admin.do("POST", "/api/control/pool", `{"miner":"n3","pool":1,"url":"stratum+tcp://public-pool.io:21496","worker":"new.worker","worker_pass":"secret","password":"wrongpass"}`); r.StatusCode != 403 {
		t.Fatalf("wrong password: %d %v", r.StatusCode, out)
	}

	if r, out := admin.do("POST", "/api/control/pool", body); r.StatusCode != 200 {
		t.Fatalf("setpool: %d %v", r.StatusCode, out)
	}
	if m.Pools[1].User != "bc1qsimulatedminer000000000000000000000" {
		t.Fatalf("pool applied before reboot: %+v", m.Pools[1])
	}

	if r, out := admin.do("POST", "/api/control/reboot", `{"miner":"n3","password":"adminpass1"}`); r.StatusCode != 200 {
		t.Fatalf("reboot: %d %v", r.StatusCode, out)
	}
	if m.Reboots != 1 {
		t.Fatalf("reboots %d", m.Reboots)
	}
	if m.Pools[1].User != "new.worker" || m.Pools[1].Pass != "secret" || m.Pools[1].URL != "stratum+tcp://public-pool.io:21496" {
		t.Fatalf("after reboot: %+v", m.Pools[1])
	}

	var sawPool, sawReboot bool
	for _, l := range m.Log() {
		if l == "setpool|admin,admin,1,stratum+tcp://public-pool.io:21496,new.worker,secret" {
			sawPool = true
		}
		if l == "ascset|0,reboot,0" {
			sawReboot = true
		}
	}
	if !sawPool || !sawReboot {
		t.Fatalf("commands not sent: pool=%v reboot=%v log=%v", sawPool, sawReboot, m.Log())
	}

	if r, out := admin.do("POST", "/api/security/view", `{"password":""}`); r.StatusCode != 200 {
		t.Fatalf("remove view password: %d %v", r.StatusCode, out)
	}
	if r, out := admin.do("POST", "/api/password", `{"current":"adminpass1","new":""}`); r.StatusCode != 200 {
		t.Fatalf("remove password: %d %v", r.StatusCode, out)
	}
	reload, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reload.Advanced {
		t.Fatal("advanced should turn off when the admin password is removed")
	}
}

func TestAdvancedRequiresPasswordToEnable(t *testing.T) {
	cfg, _ := LoadConfig(t.TempDir())
	s := &Server{Cfg: cfg, Poll: NewPoller(t.TempDir(), time.Hour), Static: web.FS}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c := &tclient{t: t, ts: ts}
	if r, out := c.do("POST", "/api/prefs", `{"advanced_controls":true}`); r.StatusCode != 400 || !strings.Contains(fmtErr(out), "admin password") {
		t.Fatalf("enable without password: %d %v", r.StatusCode, out)
	}
}

func fmtErr(out map[string]any) string {
	s, _ := out["error"].(string)
	return s
}
