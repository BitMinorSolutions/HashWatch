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
	"runtime"
	"strconv"
	"time"

	"github.com/hashwatch/hashwatch/internal/app"
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

func main() {
	host := flag.String("host", "0.0.0.0", "address to listen on (0.0.0.0 = reachable from your phone on the same Wi-Fi)")
	port := flag.Int("port", 8787, "port to listen on (the next free port is used if busy)")
	noOpen := flag.Bool("no-browser", false, "do not open the browser on start")
	dir := flag.String("config", app.DefaultDir(), "folder for settings and history")
	addMiner := flag.String("miner", "", "add a miner by IP (host[:port]) without using the setup screen")
	remote := flag.String("remote", "", "preview mode: read a Nano 3 web dashboard at this URL (read-only); credentials from HASHWATCH_REMOTE_USER / HASHWATCH_REMOTE_PASS")
	flag.Parse()

	cfg, err := app.LoadConfig(*dir)
	if err != nil {
		log.Fatal(err)
	}
	poll := app.NewPoller(*dir, 15*time.Second)
	srv := &app.Server{Cfg: cfg, Poll: poll, Static: web.FS, Version: version, LANOK: *host == "0.0.0.0" || *host == ""}

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
	fmt.Printf("\n  %s %s is running.\n\n  On this computer:  %s\n", app.AppName, version, local)
	if *host == "0.0.0.0" {
		for _, a := range app.LANAddrs() {
			fmt.Printf("  On your phone:     http://%s:%d\n", a, srv.Port)
		}
	}
	fmt.Printf("\n  Settings: %s\n  Keep this window open. Close it (or press Ctrl+C) to stop.\n\n", *dir)
	if !*noOpen && *remote == "" {
		go func() { time.Sleep(400 * time.Millisecond); openBrowser(local) }()
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
}
