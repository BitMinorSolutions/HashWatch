package app

// Finding the Pi without typing: the Mac app tries the Pi's usual addresses on
// the home network and over Tailscale, and keeps only the ones that answer as a
// HashWatch (the same /api/state check as the Test button). Only the Pi is
// contacted, never a miner.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// PiPort is the port HashWatch listens on (on the Pi too).
const PiPort = "48412"

// PiCandidate is one address to try. Via is "lan" (home network) or "tailscale".
type PiCandidate struct {
	URL string
	Via string
}

// PiFound is a candidate that answered as a HashWatch.
type PiFound struct {
	URL     string `json:"url"`
	Via     string `json:"via"`
	Version string `json:"version"`
}

// piFindTimeout bounds each probe (a missing .local name can take a few seconds to fail on macOS).
var piFindTimeout = 4 * time.Second

// tailscaleCLIs are where the Tailscale command line lives on a Mac or Windows PC. Apps opened
// from Finder don't get the shell's PATH, so the usual spots are listed explicitly.
var tailscaleCLIs = []string{
	"tailscale",
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/usr/local/bin/tailscale",
	"/opt/homebrew/bin/tailscale",
	windowsTailscale, // Windows ("tailscale" above also finds tailscale.exe on PATH)
}

const windowsTailscale = `C:\Program Files\Tailscale\tailscale.exe`

// tailscaleStatus returns `tailscale status --json` from the first CLI that works (nil if none).
var tailscaleStatus = func(ctx context.Context) []byte {
	for _, c := range tailscaleCLIs {
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		out, err := exec.CommandContext(cctx, p, "status", "--json").Output()
		cancel()
		if err == nil && len(out) > 0 {
			return out
		}
	}
	return nil
}

// tailscalePeers picks the devices named hashwatch* out of `tailscale status --json`
// and returns their MagicDNS name and Tailscale IPv4 address as candidates.
func tailscalePeers(status []byte) []PiCandidate {
	var st struct {
		Peer map[string]struct {
			HostName     string
			DNSName      string
			TailscaleIPs []string
			Online       bool
		}
	}
	if json.Unmarshal(status, &st) != nil {
		return nil
	}
	var online, offline []PiCandidate
	for _, p := range st.Peer {
		dns := strings.TrimSuffix(p.DNSName, ".")
		first, _, _ := strings.Cut(dns, ".")
		if !strings.HasPrefix(strings.ToLower(p.HostName), "hashwatch") && !strings.HasPrefix(strings.ToLower(first), "hashwatch") {
			continue
		}
		var cs []PiCandidate
		if dns != "" {
			cs = append(cs, PiCandidate{URL: "http://" + dns + ":" + PiPort, Via: "tailscale"})
		}
		for _, ip := range p.TailscaleIPs {
			if a := net.ParseIP(ip); a != nil && a.To4() != nil {
				cs = append(cs, PiCandidate{URL: "http://" + ip + ":" + PiPort, Via: "tailscale"})
			}
		}
		if p.Online {
			online = append(online, cs...)
		} else {
			offline = append(offline, cs...)
		}
	}
	return append(online, offline...)
}

// DefaultPiCandidates: the Pi's home-network name first, then its Tailscale
// addresses (from the Tailscale app if it's installed), then the MagicDNS short name.
func DefaultPiCandidates(ctx context.Context) []PiCandidate {
	cs := []PiCandidate{{URL: "http://hashwatch.local:" + PiPort, Via: "lan"}}
	if st := tailscaleStatus(ctx); st != nil {
		cs = append(cs, tailscalePeers(st)...)
	}
	return append(cs, PiCandidate{URL: "http://hashwatch:" + PiPort, Via: "tailscale"})
}

// FindPi tries every candidate at once and returns the ones that answer as a
// HashWatch: the first working home-network address, then the first working
// Tailscale address (at most one of each, in candidate order).
func FindPi(ctx context.Context, cands []PiCandidate) []PiFound {
	res := make([]*PiFound, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c PiCandidate) {
			defer wg.Done()
			info, err := testPiTimeout(ctx, c.URL, piFindTimeout)
			if err == nil {
				res[i] = &PiFound{URL: info.URL, Via: c.Via, Version: info.Version}
			}
		}(i, c)
	}
	wg.Wait()
	var out []PiFound
	for _, via := range []string{"lan", "tailscale"} {
		for _, f := range res {
			if f != nil && f.Via == via {
				out = append(out, *f)
				break
			}
		}
	}
	return out
}

func testPiTimeout(ctx context.Context, raw string, d time.Duration) (PiInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return testPi(cctx, raw)
}

// piCandidates: the candidate list (tests and the preview harness can replace it).
func (s *Server) piCandidates(ctx context.Context) []PiCandidate {
	if s.Pi != nil && s.Pi.Candidates != nil {
		return s.Pi.Candidates(ctx)
	}
	return DefaultPiCandidates(ctx)
}

// FindPi looks for the Pi (Connect to my Pi, Change address).
func (s *Server) FindPi(ctx context.Context) []PiFound {
	return FindPi(ctx, s.piCandidates(ctx))
}

// ResolvePi picks the address to show in Pi mode: the saved one if it answers,
// otherwise whichever other address of the Pi works right now (home network vs
// Tailscale), so the app works at home and away. "" means the Pi wasn't found.
func (s *Server) ResolvePi(ctx context.Context) string {
	saved, on := s.PiActive()
	if !on {
		return ""
	}
	use := ""
	if _, err := testPiTimeout(ctx, saved, piFindTimeout); err == nil {
		use = saved
	} else if found := s.FindPi(ctx); len(found) > 0 {
		use = found[0].URL
	}
	s.Pi.mu.Lock()
	s.Pi.cur = use
	s.Pi.mu.Unlock()
	return use
}

// PiCurrent is the Pi address in use this session if it differs from the saved
// one (found automatically), else "".
func (s *Server) PiCurrent() string {
	if s.Pi == nil {
		return ""
	}
	s.Pi.mu.Lock()
	defer s.Pi.mu.Unlock()
	return s.Pi.cur
}

func (s *Server) piFind(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	found := s.FindPi(ctx)
	if found == nil {
		found = []PiFound{}
	}
	writeJSON(w, 200, map[string]any{"found": found})
}

func (s *Server) piResolve(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	u := s.ResolvePi(ctx)
	if u == "" {
		saved, _ := s.PiActive()
		host := strings.TrimPrefix(strings.TrimPrefix(saved, "http://"), "https://")
		writeJSON(w, 422, map[string]string{"error": "Couldn't reach " + host + " or find your Pi at its other addresses. Check that the Pi is on and on the same network (or that Tailscale is on)."})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "url": u})
}
