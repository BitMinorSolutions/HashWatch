package cgminer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Control commands. These are the ONLY write commands HashWatch can send, and only
// through the typed functions below (never from free-form input):
//
//	ascset|0,workmode,set,N              N: 0=Low 1=Mid 2=High
//	ascset|0,ledset,E-B-T-R-G-B          E effect 0=off 1=on 2=flash 3=breath 4=loop,
//	                                     B brightness 0-100, T colour temperature 0-100, RGB 0-255
//	switchpool|N                         N: 0-15, an already-configured pool (no URL or
//	                                     credential changes). Standard cgminer API; not
//	                                     documented in Canaan's Nano 3S / Avalon Q reference
//	                                     and unverified on a real Nano 3.
//	setpool|user,pass,N,url,worker,pass  N: 0-2. Canaan's Nano 3S / Mini 3 / Avalon Q
//	                                     docs (https://www.canaan.io/resource/api/avalon-nano-3s).
//	                                     user/pass are the miner's web login (default admin,admin).
//	                                     Takes effect only after a reboot. Untested on a real Nano 3.
//	ascset|0,reboot,0                    Canaan's documented reboot for Nano 3S / Mini 3 / Avalon Q.
//	                                     Untested on a real Nano 3.
//	ascset|0,fan-spd,N                   N: -1 = automatic, or 40-100 for a fixed percent.
//	                                     Canaan documents 15-100 and -1 for Mini 3 and Avalon Q.
//	                                     HashWatch only sends -1 or 40-100. Offered on the Nano 3S
//	                                     (and Mini 3 / Q). Hidden on the original Nano 3, where
//	                                     the command is not in Canaan's docs or our fixtures.
//
// They are sent in cgminer's JSON form, e.g. {"command":"switchpool","parameter":"1"}.

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

// Fan speed: Auto is -1; Cooler is a fixed percent. Canaan's API allows 15-100;
// HashWatch only sends 40-100 (or -1).
const (
	FanAuto    = -1
	FanMinPct  = 40
	FanMaxPct  = 100
	FanDefault = 50
)

// FanSpdParam builds the ascset parameter for a fan speed.
func FanSpdParam(speed int) (string, error) {
	if speed == FanAuto {
		return "0,fan-spd,-1", nil
	}
	if speed < FanMinPct || speed > FanMaxPct {
		return "", errors.New("fan speed must be auto (-1) or 40–100")
	}
	return fmt.Sprintf("0,fan-spd,%d", speed), nil
}

// ParseFanSpdParam accepts only "0,fan-spd,-1" or "0,fan-spd,N" with N 40-100
// in canonical decimal form (no leading zeros, signs, spaces or extra fields).
func ParseFanSpdParam(param string) (int, error) {
	const pfx = "0,fan-spd,"
	if param == pfx+"-1" {
		return FanAuto, nil
	}
	if !strings.HasPrefix(param, pfx) {
		return 0, errors.New("not a fan-spd parameter")
	}
	rest := param[len(pfx):]
	if rest == "" || rest[0] < '1' || rest[0] > '9' {
		return 0, errors.New("fan speed must be auto (-1) or 40–100")
	}
	n, err := strconv.Atoi(rest)
	if err != nil || strconv.Itoa(n) != rest {
		return 0, errors.New("fan speed must be auto (-1) or 40–100")
	}
	if n < FanMinPct || n > FanMaxPct {
		return 0, errors.New("fan speed must be auto (-1) or 40–100")
	}
	return n, nil
}

func SetFanSpd(addr string, speed int) (Result, error) {
	p, err := FanSpdParam(speed)
	if err != nil {
		return Result{}, err
	}
	return writeCmd(addr, "ascset", p, 5*time.Second)
}

// The only write parameters HashWatch ever sends: the three documented work modes,
// ledset, and fan-spd (-1 or 40-100). Checked again right before sending, whatever built the parameter.
var allowedASC = regexp.MustCompile(`^0,(workmode,set,[012]|ledset,[0-4]-(100|[0-9]{1,2})-(100|[0-9]{1,2})-[0-9]{1,3}-[0-9]{1,3}-[0-9]{1,3})$`)

// AllowedWrite reports whether an ascset parameter is on the allow-list (exported for tests).
func AllowedWrite(param string) bool {
	if _, err := ParseFanSpdParam(param); err == nil {
		return true
	}
	if !allowedASC.MatchString(param) {
		return false
	}
	if strings.Contains(param, "ledset") {
		for _, v := range strings.Split(strings.SplitN(param, "ledset,", 2)[1], "-")[3:] {
			var n int
			fmt.Sscanf(v, "%d", &n)
			if n > 255 {
				return false
			}
		}
	}
	return true
}

// allowedSwitch is a pool index already on the miner: 0-15, no leading zeros or extras.
var allowedSwitch = regexp.MustCompile(`^(1[0-5]|[0-9])$`)

// RebootParam is the documented Canaan reboot (Nano 3S / Mini 3 / Avalon Q).
const RebootParam = "0,reboot,0"

// AllowedCommand is the write-command allow-list: workmode, ledset, fan-spd, switchpool,
// setpool and reboot (the last two are still rejected by the server unless
// Advanced miner controls is on).
func AllowedCommand(cmd, param string) bool {
	switch strings.ToLower(cmd) {
	case "ascset":
		return AllowedWrite(param) || param == RebootParam
	case "switchpool":
		return allowedSwitch.MatchString(param)
	case "setpool":
		return AllowedSetPool(param)
	default:
		return false
	}
}

func poolParam(n int) (string, error) {
	if n < 0 || n > 15 {
		return "", errors.New("pool number must be 0-15")
	}
	return fmt.Sprintf("%d", n), nil
}

// writeCmd sends one allow-listed write command and interprets the STATUS reply.
func writeCmd(addr, cmd, param string, timeout time.Duration) (Result, error) {
	res := Result{Sent: cmd}
	if param != "" {
		res.Sent += "|" + param
	}
	if !AllowedCommand(cmd, param) {
		return res, fmt.Errorf("refusing to send %q: command is not on the allow-list", res.Sent)
	}
	defer lockAddr(addr)()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return res, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout + 6*time.Second))
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
	return writeCmd(addr, "ascset", p, 5*time.Second)
}

func SetLED(addr string, effect, bright, temper int, hex string) (Result, error) {
	p, err := LEDParam(effect, bright, temper, hex)
	if err != nil {
		return Result{}, err
	}
	return writeCmd(addr, "ascset", p, 5*time.Second)
}

// SwitchPool makes already-configured pool n the active pool (cgminer switchpool|N).
// It never adds, removes or rewrites pool URLs or credentials.
func SwitchPool(addr string, n int) (Result, error) {
	p, err := poolParam(n)
	if err != nil {
		return Result{}, err
	}
	return writeCmd(addr, "switchpool", p, 5*time.Second)
}

// SetPoolParam builds Canaan's setpool parameter:
//
//	webuser,webpass,N,url,worker,workerpass
//
// Empty web login fields default to admin, as the Nano 3S / Mini 3 / Avalon Q
// docs specify. Pool index is 0-2. Commas, pipes and spaces in any field are refused.
func SetPoolParam(webUser, webPass string, pool int, url, worker, workerPass string) (string, error) {
	if strings.TrimSpace(webUser) == "" {
		webUser = "admin"
	}
	if strings.TrimSpace(webPass) == "" {
		webPass = "admin"
	}
	p := strings.Join([]string{webUser, webPass, fmt.Sprintf("%d", pool), url, worker, workerPass}, ",")
	if err := checkSetPool(p); err != nil {
		return "", err
	}
	return p, nil
}

var (
	poolWebRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	poolURLRe  = regexp.MustCompile(`^stratum\+(tcp|ssl|tls)://[A-Za-z0-9][A-Za-z0-9.-]{0,253}:\d{1,5}$`)
	poolUserRe = regexp.MustCompile(`^[A-Za-z0-9._~:@/+%=-]{1,128}$`)
	poolPassRe = regexp.MustCompile(`^[A-Za-z0-9._~:@/+%=-]*$`)
)

// AllowedSetPool reports whether a setpool parameter is on the allow-list.
func AllowedSetPool(param string) bool { return checkSetPool(param) == nil }

func checkSetPool(param string) error {
	parts := strings.Split(param, ",")
	if len(parts) != 6 {
		return errors.New("pool settings must be url, worker and password for one pool (0-2)")
	}
	webUser, webPass, idx, url, worker, wpass := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	if !poolWebRe.MatchString(webUser) || !poolWebRe.MatchString(webPass) {
		return errors.New("miner web login must be 1-64 letters, numbers, dots, underscores or dashes")
	}
	if idx != "0" && idx != "1" && idx != "2" {
		return errors.New("pool number must be 0, 1 or 2")
	}
	if !poolURLRe.MatchString(url) {
		return errors.New("pool URL must look like stratum+tcp://host:port")
	}
	var port int
	if i := strings.LastIndex(url, ":"); i >= 0 {
		fmt.Sscanf(url[i+1:], "%d", &port)
	}
	if port < 1 || port > 65535 {
		return errors.New("pool URL port must be 1-65535")
	}
	if !poolUserRe.MatchString(worker) {
		return errors.New("worker name has a character that isn't allowed")
	}
	if len(wpass) > 128 || !poolPassRe.MatchString(wpass) {
		return errors.New("worker password has a character that isn't allowed")
	}
	return nil
}

// SetPool changes pool N's URL, worker and password (Canaan setpool). The miner
// will not use the new settings until it is rebooted. Untested on a real Nano 3.
func SetPool(addr, webUser, webPass string, pool int, url, worker, workerPass string) (Result, error) {
	p, err := SetPoolParam(webUser, webPass, pool, url, worker, workerPass)
	if err != nil {
		return Result{}, err
	}
	return writeCmd(addr, "setpool", p, 5*time.Second)
}

// Reboot asks the miner to restart (Canaan ascset|0,reboot,0). Untested on a real Nano 3.
func Reboot(addr string) (Result, error) {
	return writeCmd(addr, "ascset", RebootParam, 8*time.Second)
}
