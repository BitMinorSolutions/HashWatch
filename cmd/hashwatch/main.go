// Command hashwatch is a local, read-only dashboard for Avalon miners.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/hashwatch/hashwatch/internal/app"
	"github.com/hashwatch/hashwatch/internal/autostart"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/notify"
	"github.com/hashwatch/hashwatch/internal/tunnel"
	"github.com/hashwatch/hashwatch/web"
)

var version = "dev"

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func main() {
	host := flag.String("host", "0.0.0.0", "address to listen on (0.0.0.0 = reachable from your phone on the same Wi-Fi)")
	port := flag.Int("port", 8787, "port to listen on (the next free port is used if busy)")
	noOpen := flag.Bool("no-browser", false, "do not open the browser on start")
	dir := flag.String("config", app.DefaultDir(), "folder for settings and history")
	addMiner := flag.String("miner", "", "add a miner by IP (host[:port]) without using the setup screen")
	remote := flag.String("remote", "", "preview mode: read a Nano 3 web dashboard at this URL (read-only); credentials from HASHWATCH_REMOTE_USER / HASHWATCH_REMOTE_PASS")
	flag.Parse()

	// Only one copy per settings folder: running it again just opens the browser.
	if *remote == "" {
		if u, ok := app.RunningInstance(*dir); ok {
			fmt.Printf("\n  %s is already running: %s\n\n", app.AppName, u)
			if !*noOpen {
				openBrowser(u)
				time.Sleep(300 * time.Millisecond)
			}
			return
		}
	}

	cfg, err := app.LoadConfig(*dir)
	if err != nil {
		log.Fatal(err)
	}
	poll := app.NewPoller(*dir, 15*time.Second)
	srv := &app.Server{Cfg: cfg, Poll: poll, Static: web.FS, Version: version, LANOK: *host == "0.0.0.0" || *host == "", InstanceID: app.NewID()}
	alerts := app.NewAlerts(*dir)
	alerts.Enabled = cfg.AlertOn
	srv.Alerts = alerts
	poll.OnPoll = func(id, name string, snap *model.Snapshot, hist []model.Sample) { alerts.Observe(id, name, snap, hist) }

	if *remote != "" {
		srv.Locked = true
		cfg.PasswordHash, cfg.PasswordSalt = "", ""
		if pw := os.Getenv("HASHWATCH_PASSWORD"); pw != "" {
			cfg.SetPassword(pw)
		}
		cfg.Miners = []app.Miner{{ID: "remote", Name: "Avalon Nano 3", Host: "remote", Kind: "remote",
			RemoteURL: *remote, RemoteUser: os.Getenv("HASHWATCH_REMOTE_USER"), RemotePass: os.Getenv("HASHWATCH_REMOTE_PASS")}}
	} else if *addMiner != "" {
		h, p, err := net.SplitHostPort(*addMiner)
		pn := 4028
		if err != nil {
			h = *addMiner
		} else {
			pn, _ = strconv.Atoi(p)
		}
		cfg.Miners = append(cfg.Miners, app.Miner{ID: app.NewID(), Name: "Avalon " + h, Host: h, Port: pn, Kind: "cgminer"})
		cfg.Save()
	}
	if *remote == "" {
		alerts.Notify = func(a app.Alert) {
			if cfg.NotifyOff {
				return
			}
			title := a.Title
			if a.MinerName != "" && a.Kind == "block" {
				title = a.Title + " · " + a.MinerName
			}
			if err := notify.Send(title, a.Body); err != nil {
				log.Printf("desktop notification: %v", err)
			}
		}
	}
	for _, m := range cfg.Miners {
		poll.Start(m)
	}

	var ln net.Listener
	for p := *port; p < *port+20; p++ {
		if ln, err = net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(p))); err == nil {
			srv.Port = p
			break
		}
	}
	if ln == nil {
		log.Fatal(err)
	}
	local := fmt.Sprintf("http://localhost:%d", srv.Port)
	originHost := "127.0.0.1"
	if *host != "0.0.0.0" && *host != "" && *host != "localhost" && *host != "::" {
		originHost = *host
	}
	if *remote == "" {
		srv.Tunnel = &tunnel.Manager{Dir: filepath.Join(*dir, "cloudflared"), Origin: "http://" + net.JoinHostPort(originHost, strconv.Itoa(srv.Port)),
			Token: cfg.Remote.Token, Hostname: cfg.Remote.Hostname, OnURL: srv.TunnelURL}
		args := []string{"-no-browser", "-config", absPath(*dir)}
		if *host != "0.0.0.0" {
			args = append(args, "-host", *host)
		}
		if *port != 8787 {
			args = append(args, "-port", strconv.Itoa(*port))
		}
		srv.Auto = autostart.Spec{Exe: autostart.Exe(), Args: args, Dir: absPath(*dir), AppName: app.AppName}
		if cfg.Remote.Enabled && cfg.PasswordHash != "" {
			srv.Tunnel.Start()
			fmt.Println("  Remote access: starting…")
		}
		app.WriteInstance(*dir, srv.Port, srv.InstanceID, local)
	}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		if srv.Tunnel != nil {
			srv.Tunnel.Stop()
		}
		poll.SaveNow()
		if *remote == "" {
			app.RemoveInstance(*dir, srv.InstanceID)
		}
		os.Exit(0)
	}()
	fmt.Printf("\n  %s %s is running.\n\n  On this computer:  %s\n", app.AppName, version, local)
	if *host == "0.0.0.0" {
		for _, a := range app.LANAddrs() {
			fmt.Printf("  On your phone:     http://%s:%d\n", a, srv.Port)
		}
	}
	fmt.Printf("\n  Settings: %s\n  Keep this window open. Close it (or press Ctrl+C) to stop.\n  Running %s again just opens your browser to this copy.\n\n", *dir, app.AppName)
	if !*noOpen && *remote == "" {
		go func() { time.Sleep(400 * time.Millisecond); openBrowser(local) }()
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
}
