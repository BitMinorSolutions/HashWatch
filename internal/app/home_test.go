package app

import (
	"net"
	"testing"
)

// fakeNets makes the "Pi" look like it has these interface subnets.
func fakeNets(t *testing.T, cidrs ...string) {
	t.Helper()
	var nets []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		nets = append(nets, n)
	}
	old := localNets
	localNets = func() []*net.IPNet { return nets }
	resetLocalNets()
	t.Cleanup(func() { localNets = old; resetLocalNets() })
}

const piV6Net = "2600:1700:2534:c9b0::/64" // AT&T-style global prefix on the Pi's Wi-Fi/Ethernet

func TestIsHomeIP(t *testing.T) {
	fakeNets(t, "192.168.1.0/24", piV6Net, "203.0.113.0/24")
	cases := map[string]bool{
		"127.0.0.1": true, "::1": true,
		"192.168.1.50": true, "10.1.2.3": true, "172.20.0.1": true, "::ffff:192.168.1.50": true,
		"169.254.1.1": true, "fe80::1": true,
		"fd12:3456::1": true, "fd7a:115c:a1e0::1": true, // ULA, Tailscale IPv6
		"100.121.124.114":                        true,  // Tailscale
		"2600:1700:2534:c9b0:883:604f:e1cf:fd45": true,  // same /64 as the Pi (the user's Mac at home)
		"203.0.113.77":                           true,  // public IPv4, but on the Pi's own subnet
		"2600:1700:2534:c9b1::5":                 false, // neighbouring /64: not on-link
		"2600:1700:9999:1::1":                    false,
		"8.8.8.8":                                false, "100.128.0.1": false, "198.51.100.1": false,
		"2001:4860:4860::8888": false, "::": false, "ff02::1": false,
	}
	for s, want := range cases {
		if got := IsHomeIP(net.ParseIP(s)); got != want {
			t.Errorf("IsHomeIP(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestInterfaceNetsSkipsWidePrefixes(t *testing.T) {
	for _, n := range interfaceNets() {
		ones, bits := n.Mask.Size()
		if n.IP.IsLoopback() || (bits == 32 && ones < 8) || (bits == 128 && ones < 32) {
			t.Fatalf("unexpected net %v", n)
		}
	}
}

func TestFirstPasswordFromGlobalIPv6OnLink(t *testing.T) {
	fakeNets(t, "192.168.1.0/24", piV6Net)
	s, c := firstPwServer(t)
	mac := "[2600:1700:2534:c9b0:883:604f:e1cf:fd45]:52000"
	_, st := c.as(s, mac, "GET", "/api/state", "", nil)
	if st["password_first"] != true || st["password_first_ok"] != true {
		t.Fatalf("state: %v", st)
	}
	if r, out := c.as(s, mac, "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 200 {
		t.Fatalf("first set: %d %v", r.StatusCode, out)
	}
	// The next step, Connect miner, uses the same home-network rule. A bad
	// host gets past the guard and fails in the handler instead of a 403.
	r, out := c.as(s, mac, "POST", "/api/miners", `{"host":""}`, nil)
	if r.StatusCode == 403 || r.StatusCode == 401 {
		t.Fatalf("connect miner blocked: %d %v", r.StatusCode, out)
	}
	if r, out := c.as(s, mac, "POST", "/api/prefs", `{"gentle":true}`, nil); r.StatusCode != 200 {
		t.Fatalf("admin write: %d %v", r.StatusCode, out)
	}
}

func TestFirstPasswordFromGlobalIPv6OffLinkRejected(t *testing.T) {
	fakeNets(t, "192.168.1.0/24", piV6Net)
	for _, addr := range []string{"[2600:1700:2534:c9b1::5]:1", "[2001:4860:4860::8888]:1"} {
		s, c := firstPwServer(t)
		_, st := c.as(s, addr, "GET", "/api/state", "", nil)
		if st["password_first_ok"] != false {
			t.Fatalf("%s state: %v", addr, st)
		}
		if r, out := c.as(s, addr, "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 403 {
			t.Fatalf("%s: %d %v", addr, r.StatusCode, out)
		}
		if s.Cfg.PasswordHash != "" {
			t.Fatalf("%s set a password", addr)
		}
	}
}

func TestOnLinkIPv4AndTunnel(t *testing.T) {
	fakeNets(t, "203.0.113.0/24", piV6Net)
	s, c := firstPwServer(t)
	// On-link but through the tunnel: refused.
	h := map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}
	if r, _ := c.as(s, "[2600:1700:2534:c9b0::9]:1", "POST", "/api/password", `{"new":"hunter22"}`, h); r.StatusCode == 200 {
		t.Fatal("tunnel request set a password")
	}
	// Public IPv4 off the subnet: refused.
	if r, _ := c.as(s, "198.51.100.1:1", "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 403 {
		t.Fatalf("off-subnet v4: %d", r.StatusCode)
	}
	// Same IPv4 subnet as the Pi (even a public one): accepted.
	if r, out := c.as(s, "203.0.113.50:1", "POST", "/api/password", `{"new":"hunter22"}`, nil); r.StatusCode != 200 {
		t.Fatalf("on-subnet v4: %d %v", r.StatusCode, out)
	}
}
