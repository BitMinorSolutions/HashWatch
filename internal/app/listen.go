package app

import (
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

// AppID identifies a HashWatch server on /api/ping, independent of the display name.
const AppID = "app.hashwatch.server"

// DefaultPort is deliberately uncommon so HashWatch rarely meets another program's port.
// Copies before 0.5.1 used 8787 (8813 for the private preview); those values are not reused.
const DefaultPort = 48412

// ListenHost is the bind address for the web server: loopback only, or every
// interface when phones on Wi-Fi are allowed.
func ListenHost(allowWifi bool) string {
	if allowWifi {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// WildcardHost reports whether host is an "all interfaces" bind address.
func WildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// ResolveListenHost picks the bind address. An explicit --host wins; a private
// preview is always loopback; otherwise the Allow phones on Wi-Fi setting is used.
func ResolveListenHost(explicit string, allowWifi, preview bool) string {
	if preview {
		return "127.0.0.1"
	}
	if explicit != "" {
		return explicit
	}
	return ListenHost(allowWifi)
}

// legacyPorts are defaults of older versions: a saved port in this range is ignored on upgrade.
func legacyPort(p int) bool { return (p >= 8787 && p < 8807) || (p >= 8813 && p < 8833) }

// PortCandidates is the order ports are tried in: an explicit -port, then the port this copy
// used last time, then preferred and the next nine; Listen falls back to an OS-chosen port.
func PortCandidates(explicit, saved, preferred int) []int {
	var out []int
	seen := map[int]bool{}
	add := func(p int) {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(explicit)
	if !legacyPort(saved) {
		add(saved)
	}
	for i := 0; i < 10; i++ {
		add(preferred + i)
	}
	return out
}

// loopbackFor is the address this copy itself is reached at (window, menu bar, tunnel).
func loopbackFor(host string) string {
	switch host {
	case "", "0.0.0.0", "::", "localhost", "127.0.0.1":
		return "127.0.0.1"
	}
	return host
}

// portAnswers reports whether some program already accepts connections on this port
// on the loopback addresses (IPv4 or IPv6), even if a wildcard bind would still succeed.
func portAnswers(host string, port int) bool {
	addrs := []string{loopbackFor(host)}
	if addrs[0] == "127.0.0.1" {
		addrs = append(addrs, "::1")
	}
	for _, a := range addrs {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(a, strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// reachesUs proves that connecting to the address the window/menu bar/tunnel use lands on
// this very listener: macOS (BSD sockets) lets a wildcard bind succeed while another
// program holds 127.0.0.1:port, and then local traffic goes to that other program.
func reachesUs(ln net.Listener, host string, port int) bool {
	nonce := "hashwatch-" + NewID() + NewID()
	if tl, ok := ln.(*net.TCPListener); ok {
		tl.SetDeadline(time.Now().Add(1500 * time.Millisecond))
		defer tl.SetDeadline(time.Time{})
	}
	got := make(chan bool, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				got <- false
				return
			}
			c.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, len(nonce))
			_, err = io.ReadFull(c, buf)
			c.Close()
			if err == nil && string(buf) == nonce {
				got <- true
				return
			}
			// someone else connected meanwhile (e.g. a phone): keep waiting for our probe
		}
	}()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(loopbackFor(host), strconv.Itoa(port)), time.Second)
	if err == nil {
		c.SetWriteDeadline(time.Now().Add(time.Second))
		c.Write([]byte(nonce))
		defer c.Close()
	}
	return <-got
}

// Listen binds HashWatch's web server. A port is used only if nothing else answers on it
// and a probe to 127.0.0.1:port verifiably reaches this listener; otherwise the next
// candidate is tried, then a port chosen by the OS. Other programs are never touched.
func Listen(host string, candidates []int) (net.Listener, int, error) {
	var lastErr error = errors.New("no port available")
	try := func(p int) (net.Listener, int, bool) {
		if p != 0 && portAnswers(host, p) {
			log.Printf("port %d is used by another program: skipping it", p)
			return nil, 0, false
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err != nil {
			lastErr = err
			return nil, 0, false
		}
		bound := ln.Addr().(*net.TCPAddr).Port
		if !reachesUs(ln, host, bound) {
			log.Printf("port %d: local connections reach another program: skipping it", bound)
			ln.Close()
			lastErr = errors.New("port " + strconv.Itoa(bound) + " is shadowed by another program")
			return nil, 0, false
		}
		return ln, bound, true
	}
	for _, p := range candidates {
		if ln, port, ok := try(p); ok {
			return ln, port, nil
		}
	}
	for i := 0; i < 5; i++ { // let the OS choose
		if ln, port, ok := try(0); ok {
			return ln, port, nil
		}
	}
	return nil, 0, lastErr
}
