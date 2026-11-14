package app

import (
	"net"
	"sort"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
)

type Found struct {
	Host string `json:"host"`
	Desc string `json:"desc"`
}

// localSubnets returns the /24 prefixes of private IPv4 interfaces.
func localSubnets() []net.IP {
	var out []net.IP
	seen := map[string]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || !ip.IsPrivate() {
			continue
		}
		base := net.IPv4(ip[0], ip[1], ip[2], 0).To4()
		if !seen[base.String()] {
			seen[base.String()] = true
			out = append(out, base)
		}
	}
	return out
}

// Discover scans each local /24 for TCP 4028 and confirms with a read-only "version".
func Discover() []Found {
	var mu sync.Mutex
	var res []Found
	sem := make(chan struct{}, 128)
	var wg sync.WaitGroup
	for _, base := range localSubnets() {
		for i := 1; i < 255; i++ {
			host := net.IPv4(base[0], base[1], base[2], byte(i)).String()
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				addr := net.JoinHostPort(host, "4028")
				c, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
				if err != nil {
					return
				}
				c.Close()
				desc, ok := cgminer.Probe(addr, 2*time.Second)
				if ok {
					mu.Lock()
					res = append(res, Found{Host: host, Desc: desc})
					mu.Unlock()
				}
			}()
		}
	}
	wg.Wait()
	sort.Slice(res, func(i, j int) bool { return res[i].Host < res[j].Host })
	return res
}

func LANAddrs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip := ipn.IP.To4(); ip != nil && ip.IsPrivate() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}
