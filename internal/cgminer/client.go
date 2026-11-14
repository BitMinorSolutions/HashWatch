// Package cgminer talks to the CGMiner-compatible JSON API that Avalon miners
// expose on TCP port 4028. Only read commands are ever sent.
package cgminer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sync"
	"time"
)

// ReadOnly is the allow-list of commands this package will send.
var ReadOnly = map[string]bool{"version": true, "summary": true, "pools": true, "devs": true, "estats": true, "stats": true, "litestats": true}

var trailingComma = regexp.MustCompile(`,\s*([}\]])`)

// addrLocks keeps HashWatch to one open connection per miner at a time (reads,
// controls and automations alike), so it stays gentle on the miner's small API
// server and coexists with another dashboard polling the same miner.
var addrLocks sync.Map

func lockAddr(addr string) func() {
	v, _ := addrLocks.LoadOrStore(addr, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// Query sends one read-only command and returns the decoded reply.
func Query(addr, command string, timeout time.Duration) (map[string]any, error) {
	if !ReadOnly[command] {
		return nil, fmt.Errorf("command %q is not allowed", command)
	}
	defer lockAddr(addr)()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout + 4*time.Second))
	req, _ := json.Marshal(map[string]string{"command": command})
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(conn, 4<<20))
	if err != nil && len(raw) == 0 {
		return nil, err
	}
	return decode(raw)
}

// decode tolerates the usual cgminer quirks: trailing NUL, concatenated
// objects ("}{"), trailing commas and stray control characters.
func decode(raw []byte) (map[string]any, error) {
	raw = bytes.TrimRight(raw, "\x00 \r\n\t")
	if len(raw) == 0 {
		return nil, errors.New("empty reply")
	}
	clean := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b < 0x20 && b != '\n' && b != '\t' {
			continue
		}
		clean = append(clean, b)
	}
	clean = bytes.ReplaceAll(clean, []byte("}{"), []byte("},{"))
	clean = trailingComma.ReplaceAll(clean, []byte("$1"))
	var out map[string]any
	if err := json.Unmarshal(clean, &out); err == nil {
		return out, nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(append(append([]byte("["), clean...), ']'), &arr); err == nil && len(arr) > 0 {
		merged := map[string]any{}
		for _, m := range arr {
			for k, v := range m {
				merged[k] = v
			}
		}
		return merged, nil
	}
	return nil, errors.New("could not parse miner reply")
}

// Probe returns a short description if addr answers like a cgminer API.
func Probe(addr string, timeout time.Duration) (string, bool) {
	r, err := Query(addr, "version", timeout)
	if err != nil {
		return "", false
	}
	v := first(r, "VERSION")
	if v == nil {
		return "CGMiner API", true
	}
	prod, model := str(v["PROD"]), str(v["MODEL"])
	desc := prod
	if model != "" {
		desc = prod + " " + model
	}
	if desc == "" {
		desc = "CGMiner " + str(v["CGMiner"])
	}
	return desc, true
}
