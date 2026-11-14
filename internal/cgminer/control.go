package cgminer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

// Control commands. These are the ONLY write commands Hashwatch can send, and only
// through these two typed functions (never from free-form input). Syntax per Canaan's
// public Avalon Nano 3S / Avalon Q API reference:
//
//	ascset|0,workmode,set,N              N: 0=Low 1=Mid 2=High
//	ascset|0,ledset,E-B-T-R-G-B          E effect 0=off 1=on 2=flash 3=breath 4=loop,
//	                                     B brightness 0-100, T colour temperature 0-100, RGB 0-255
//
// They are sent in cgminer's JSON form: {"command":"ascset","parameter":"0,workmode,set,2"}.

type Result struct {
	OK      bool   `json:"ok"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Sent    string `json:"sent"`
}

var hexRe = regexp.MustCompile(`^#?([0-9a-fA-F]{6})$`)

// WorkModeParam builds the ascset parameter for a work level (0..2).
func WorkModeParam(level int) (string, error) {
	if level < 0 || level > 2 {
		return "", errors.New("work level must be 0, 1 or 2")
	}
	return fmt.Sprintf("0,workmode,set,%d", level), nil
}

// LEDParam builds the ascset parameter for the light.
func LEDParam(effect, bright, temper int, hex string) (string, error) {
	if effect < 0 || effect > 4 {
		return "", errors.New("light mode must be 0-4")
	}
	if bright < 0 || bright > 100 || temper < 0 || temper > 100 {
		return "", errors.New("brightness and colour temperature must be 0-100")
	}
	m := hexRe.FindStringSubmatch(strings.TrimSpace(hex))
	if m == nil {
		return "", errors.New("colour must look like #ff6a00")
	}
	var r, g, b int
	fmt.Sscanf(m[1], "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("0,ledset,%d-%d-%d-%d-%d-%d", effect, bright, temper, r, g, b), nil
}

// ascset sends one validated ascset parameter and interprets the STATUS reply.
func ascset(addr, param string, timeout time.Duration) (Result, error) {
	res := Result{Sent: "ascset|" + param}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return res, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout + 6*time.Second))
	req, _ := json.Marshal(map[string]string{"command": "ascset", "parameter": param})
	if _, err := conn.Write(req); err != nil {
		return res, err
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)
	for {
		n, err := conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil || len(buf) > 64<<10 {
			break
		}
	}
	r, err := decode(buf)
	if err != nil {
		return res, err
	}
	st := first(r, "STATUS")
	if st == nil {
		return res, errors.New("miner sent no STATUS")
	}
	res.Status, res.Message = str(st["STATUS"]), strings.TrimSpace(str(st["Msg"]))
	res.OK = res.Status == "S" || res.Status == "I"
	return res, nil
}

func SetWorkMode(addr string, level int) (Result, error) {
	p, err := WorkModeParam(level)
	if err != nil {
		return Result{}, err
	}
	return ascset(addr, p, 5*time.Second)
}

func SetLED(addr string, effect, bright, temper int, hex string) (Result, error) {
	p, err := LEDParam(effect, bright, temper, hex)
	if err != nil {
		return Result{}, err
	}
	return ascset(addr, p, 5*time.Second)
}

// Pool and restart commands (documented in cgminer's API-README; privileged, so the
// miner's API must allow writes, which the Nano 3's does for ascset). Used only by
// opt-in automations, with typed arguments:
//
//	switchpool|N   make pool N the active pool
//	enablepool|N   enable pool N
//	restart        restart cgminer (on Avalon firmware this restarts mining; not yet verified on a Nano 3)
func command(addr, cmd, param string, timeout time.Duration) (Result, error) {
	res := Result{Sent: cmd}
	if param != "" {
		res.Sent += "|" + param
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return res, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout + 4*time.Second))
	m := map[string]string{"command": cmd}
	if param != "" {
		m["parameter"] = param
	}
	req, _ := json.Marshal(m)
	if _, err := conn.Write(req); err != nil {
		return res, err
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)
	for {
		n, err := conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil || len(buf) > 64<<10 {
			break
		}
	}
	if cmd == "restart" && len(strings.TrimRight(string(buf), "\x00 \r\n")) == 0 {
		// cgminer may close the socket while restarting; the command was delivered.
		res.OK, res.Status, res.Message = true, "RESTART", "connection closed while restarting"
		return res, nil
	}
	r, err := decode(buf)
	if err != nil {
		return res, err
	}
	st := first(r, "STATUS")
	if st == nil {
		if s, ok := r["STATUS"].(string); ok { // cgminer replies {"STATUS":"RESTART"}
			res.Status, res.OK, res.Message = s, strings.EqualFold(s, "RESTART"), s
			return res, nil
		}
		return res, errors.New("miner sent no STATUS")
	}
	res.Status, res.Message = str(st["STATUS"]), strings.TrimSpace(str(st["Msg"]))
	res.OK = res.Status == "S" || res.Status == "I"
	return res, nil
}

func poolParam(n int) (string, error) {
	if n < 0 || n > 15 {
		return "", errors.New("pool number must be 0-15")
	}
	return fmt.Sprintf("%d", n), nil
}

func SwitchPool(addr string, n int) (Result, error) {
	p, err := poolParam(n)
	if err != nil {
		return Result{}, err
	}
	return command(addr, "switchpool", p, 5*time.Second)
}

func EnablePool(addr string, n int) (Result, error) {
	p, err := poolParam(n)
	if err != nil {
		return Result{}, err
	}
	return command(addr, "enablepool", p, 5*time.Second)
}

func Restart(addr string) (Result, error) { return command(addr, "restart", "", 5*time.Second) }
