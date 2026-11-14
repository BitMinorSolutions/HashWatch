// Package source provides miner data sources: direct CGMiner (port 4028)
// and a read-only "remote" source that reads an existing Nano 3 web dashboard.
package source

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hashwatch/hashwatch/internal/cgminer"
	"github.com/hashwatch/hashwatch/internal/model"
)

type Source interface {
	Fetch() *model.Snapshot
}

// CGMiner reads a miner directly over the LAN.
type CGMiner struct{ Addr string }

func (c CGMiner) Fetch() *model.Snapshot { return cgminer.Fetch(c.Addr, 4*time.Second) }

// Remote reads an existing Avalon Nano 3 dashboard (GET /api, GET /control/state).
// It only ever performs the login POST and GETs on those two paths.
type Remote struct {
	Base, User, Pass string
	mu               sync.Mutex
	client           *http.Client
}

var csrfRe = regexp.MustCompile(`name="csrf"\s+value="([^"]+)"`)

func (r *Remote) init() {
	if r.client == nil {
		jar, _ := cookiejar.New(nil)
		r.client = &http.Client{Jar: jar, Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
}

func (r *Remote) login() error {
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	res, err := r.client.Get(r.Base + "/login")
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	m := csrfRe.FindSubmatch(body)
	if m == nil {
		return errors.New("remote login page has no csrf field")
	}
	form := url.Values{"csrf": {string(m[1])}, "username": {r.User}, "password": {r.Pass}}
	res, err = r.client.PostForm(r.Base+"/login", form)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode < 300 || res.StatusCode >= 400 || strings.Contains(res.Header.Get("Location"), "/login") {
		return fmt.Errorf("remote login rejected (HTTP %d)", res.StatusCode)
	}
	return nil
}

func (r *Remote) get(path string, out any) error {
	if path != "/api" && path != "/control/state" {
		return errors.New("path not allowed")
	}
	for attempt := 0; attempt < 2; attempt++ {
		res, err := r.client.Get(r.Base + path)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if res.StatusCode == 200 && strings.Contains(res.Header.Get("Content-Type"), "json") {
			return json.Unmarshal(body, out)
		}
		if attempt == 0 {
			if err := r.login(); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("remote HTTP %d", res.StatusCode)
	}
	return errors.New("unreachable")
}

type remoteControl struct {
	WorkLevel *int   `json:"work_level"`
	WorkName  string `json:"work_name"`
	LED       *struct {
		ModeName   string `json:"mode_name"`
		Hex        string `json:"hex"`
		Brightness *int   `json:"brightness"`
	} `json:"led"`
}

func (r *Remote) Fetch() *model.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	s := &model.Snapshot{}
	if err := r.get("/api", s); err != nil {
		return &model.Snapshot{OK: false, Source: "remote", FetchedAt: time.Now().Unix(), QueryErrors: []string{err.Error()}}
	}
	s.Source = "remote"
	if s.FetchedAt == 0 {
		s.FetchedAt = time.Now().Unix()
	}
	var c remoteControl
	if err := r.get("/control/state", &c); err == nil {
		s.Control = &model.Control{WorkLevel: c.WorkLevel, WorkName: c.WorkName}
		if c.LED != nil {
			s.Control.LEDMode, s.Control.LEDHex, s.Control.LEDBright = c.LED.ModeName, c.LED.Hex, c.LED.Brightness
		}
	} else if s.Power.WorkLevel != nil {
		s.Control = &model.Control{WorkLevel: s.Power.WorkLevel, WorkName: cgminer.WorkName(*s.Power.WorkLevel)}
	}
	return s
}
