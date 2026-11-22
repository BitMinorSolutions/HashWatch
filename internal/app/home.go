package app

import (
	"net"
	"sync"
	"time"
)

// Home network detection.
//
// A visitor is "on the home network" when its address is loopback, private
// (10/8, 172.16/12, 192.168/16, IPv6 ULA fc00::/7), link-local, Tailscale
// (100.64.0.0/10), or inside one of this machine's own interface subnets.
// The last rule matters for IPv6: many ISPs (AT&T, Comcast…) give every home
// device a global 2600:…/2001:… address, so a Mac at home reaches the Pi
// from a global address in the Pi's own /64.

var tailscaleCGNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// localNets lists this machine's interface subnets. Tests replace it.
var localNets = interfaceNets

const localNetsTTL = 30 * time.Second // prefixes can change (new ISP prefix, DHCP)

var netCache struct {
	sync.Mutex
	at   time.Time
	nets []*net.IPNet
}

func interfaceNets() []*net.IPNet {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() {
			continue
		}
		ones, bits := n.Mask.Size()
		// Ignore implausibly wide prefixes so a misconfigured interface can't
		// make most of the internet look "local".
		if bits == 32 && ones < 8 || bits == 128 && ones < 32 {
			continue
		}
		out = append(out, &net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask})
	}
	return out
}

func cachedLocalNets() []*net.IPNet {
	netCache.Lock()
	defer netCache.Unlock()
	if netCache.nets == nil || time.Since(netCache.at) > localNetsTTL {
		netCache.nets, netCache.at = localNets(), time.Now()
	}
	return netCache.nets
}

func resetLocalNets() {
	netCache.Lock()
	netCache.nets = nil
	netCache.Unlock()
}

// onLink: ip is inside one of this machine's interface subnets.
func onLink(ip net.IP) bool {
	for _, n := range cachedLocalNets() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// IsHomeIP reports whether a peer address counts as the home network.
func IsHomeIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4 // IPv4-mapped IPv6 (::ffff:a.b.c.d) from a dual-stack listener
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || tailscaleCGNAT.Contains(ip) {
		return true
	}
	return onLink(ip)
}
