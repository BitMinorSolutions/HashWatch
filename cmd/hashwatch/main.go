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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashwatch/hashwatch/internal/app"
	"github.com/hashwatch/hashwatch/internal/autostart"
	"github.com/hashwatch/hashwatch/internal/install"
	"github.com/hashwatch/hashwatch/internal/model"
	"github.com/hashwatch/hashwatch/internal/notify"
	"github.com/hashwatch/hashwatch/internal/tunnel"
	"github.com/hashwatch/hashwatch/web"
)

var version = "dev"

// bundleGOOS is the OS whose .app layout triggers launcher mode (overridable in tests).
var bundleGOOS = "darwin"

// Private builds can bake in a read-only preview source (set with -ldflags -X, never committed):
// they skip setup, listen on 127.0.0.1, and use their own port and settings folder.
var bakedRemote, bakedUser, bakedPass, bakedPort, bakedDirName string

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

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// inAppBundle reports whether we're the executable inside Hashwatch.app.
func inAppBundle() bool {
	return runtime.GOOS == bundleGOOS && strings.Contains(autostart.Exe(), ".app/Contents/MacOS/")
}

// showRunning asks an already running copy to bring its window to the front.
func showRunning(u string) bool {
	req, _ := http.NewRequest("POST", strings.TrimRight(u, "/")+"/api/show", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", strings.TrimRight(u, "/"))
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200 || resp.StatusCode == 204
}

func main() {
	defHost, defPort, defDir := "0.0.0.0", app.DefaultPort, app.DefaultDir()
	if bakedRemote != "" {
		defHost = "127.0.0.1"
	}
	if n, err := strconv.Atoi(bakedPort); err == nil {
		defPort = n
	}
	if bakedDirName != "" {
		defDir = filepath.Join(filepath.Dir(app.DefaultDir()), bakedDirName)
	}
	host := flag.String("host", defHost, "address to listen on (0.0.0.0 = reachable from your phone on the same Wi-Fi)")
	port := flag.Int("port", 0, fmt.Sprintf("port to listen on (default: the port used last time, else %d; another free port is picked automatically if one is taken)", defPort))
	noOpen := flag.Bool("no-browser", false, "do not open the browser on start")
	dir := flag.String("config", defDir, "folder for settings and history")
	addMiner := flag.String("miner", "", "add a miner by IP (host[:port]) without using the setup screen")
	remote := flag.String("remote", bakedRemote, "preview mode: read a Nano 3 web dashboard at this URL (read-only); credentials from HASHWATCH_REMOTE_USER / HASHWATCH_REMOTE_PASS")
	// Older macOS passes a -psn_… process serial number to apps opened from Finder.
	for i := 1; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "-psn_") {
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			i--
		}
	}
	every := flag.Duration("poll", app.DefaultPoll, "how often to read each miner (failed reads back off to 15s, then 30s)")
	serve := flag.Bool("serve", false, "run the server in this process with no app window (browser only)")
	hidden := flag.Bool("hidden", false, "start in the menu bar / system tray without opening a window (used by auto-start)")
	flag.Parse()
	private := bakedRemote != "" && *remote == bakedRemote // baked private preview: full app lifecycle
	notify.AppName = app.AppName
	// Native shell: HashWatch.app on macOS (window + menu bar) and HashWatch.exe on Windows (window + tray).
	native := nativeSupported() && !*serve && (*remote == "" || private)
	if native {
		if err := os.MkdirAll(*dir, 0o755); err == nil {
			if logf, err := os.OpenFile(filepath.Join(*dir, "hashwatch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				log.SetOutput(logf)
				os.Stdout, os.Stderr = logf, logf
			}
		}
	}

	// Inside Hashwatch.app the visible executable is only a launcher: it starts the
	// server as a detached background process and exits, so macOS shows no Terminal,
	// and double-clicking the app again launches a fresh launcher that just reopens
	// the browser (a still-running bundle would swallow the second double-click).
	if !native && inAppBundle() && !*serve && (*remote == "" || private) {
		if u, ok := app.RunningInstance(*dir); ok {
			if !*noOpen {
				openBrowser(u)
			}
			return
		}
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			log.Fatal(err)
		}
		logf, err := os.OpenFile(filepath.Join(*dir, "hashwatch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatal(err)
		}
		cmd := exec.Command(autostart.Exe(), append([]string{"-serve"}, os.Args[1:]...)...)
		cmd.Stdout, cmd.Stderr = logf, logf
		detach(cmd)
		if err := cmd.Start(); err != nil {
			log.Fatal(err)
		}
		_ = cmd.Process.Release()
		return
	}

	// Only one copy per settings folder: running it again just opens the browser.
	if *remote == "" || private {
		if u, ok := app.RunningInstance(*dir); ok {
			fmt.Printf("\n  %s is already running: %s\n\n", app.AppName, u)
			if native { // bring the running copy's window forward
				if *hidden || showRunning(u) {
					return
				}
			}
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
	if *every < 2*time.Second {
		*every = 2 * time.Second
	}
	poll := app.NewPoller(*dir, *every)
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
			RemoteURL: *remote, RemoteUser: envOr("HASHWATCH_REMOTE_USER", bakedUser), RemotePass: envOr("HASHWATCH_REMOTE_PASS", bakedPass)}}
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

	// Bind only a port that verifiably reaches this process (never one another program answers on).
	ln, bound, err := app.Listen(*host, app.PortCandidates(*port, cfg.Port, defPort))
	if err != nil {
		log.Fatal(err)
	}
	srv.Port = bound
	if *remote == "" || private {
		if cfg.Port != bound {
			cfg.Port = bound
			cfg.Save()
		}
		if *port == 0 && bound != defPort {
			log.Printf("preferred port %d was busy: using %d", defPort, bound)
		}
	}
	local := app.LocalURL(srv.Port)
	originHost := "127.0.0.1"
	if *host != "0.0.0.0" && *host != "" && *host != "localhost" && *host != "::" {
		originHost = *host
	}
	if *remote == "" {
		srv.Tunnel = &tunnel.Manager{Dir: filepath.Join(*dir, "cloudflared"), Origin: "http://" + net.JoinHostPort(originHost, strconv.Itoa(srv.Port)),
			Token: cfg.Remote.Token, Hostname: cfg.Remote.Hostname, OnURL: srv.TunnelURL}
		args := []string{"-serve", "-no-browser", "-config", absPath(*dir)}
		if nativeSupported() {
			args = []string{"-hidden", "-config", absPath(*dir)} // starts quietly in the menu bar / tray
		}
		if *host != "0.0.0.0" {
			args = append(args, "-host", *host)
		}
		if *port != 0 {
			args = append(args, "-port", strconv.Itoa(*port))
		}
		srv.Auto = autostart.Spec{Exe: autostart.Exe(), Args: args, Dir: absPath(*dir), AppName: app.AppName}
		if cfg.Remote.Enabled && cfg.PasswordHash != "" {
			srv.Tunnel.Start()
			fmt.Println("  Remote access: starting…")
		}
	}
	if *remote == "" || private {
		app.WriteInstance(*dir, srv.Port, srv.InstanceID)
	}
	var quitOnce sync.Once
	shutdown := func() {
		quitOnce.Do(func() {
			log.Printf("%s: stopping", app.AppName)
			if srv.Tunnel != nil {
				srv.Tunnel.Stop()
			}
			poll.SaveNow()
			if *remote == "" || private {
				app.RemoveInstance(*dir, srv.InstanceID)
			}
			os.Exit(0)
		})
	}
	if *remote == "" || private {
		srv.Quit = shutdown
	}
	if inAppBundle() && (*remote == "" || private) {
		home, _ := os.UserHomeDir()
		srv.Install = install.Check(autostart.Exe(), home)
		srv.Relaunch = func(dst string) {
			// Wait for this process to exit (port + instance file freed), then open the installed copy.
			// Same flags as this copy (settings folder, port…), minus -serve, plus -no-browser:
			// the open tab reconnects by itself.
			args := []string{"-no-browser"}
			for i := 1; i < len(os.Args); i++ {
				switch a := os.Args[i]; a {
				case "-serve", "--serve", "-no-browser", "--no-browser":
				case "-miner", "--miner": // already saved in settings
					i++
				default:
					if !strings.HasPrefix(a, "-miner=") && !strings.HasPrefix(a, "--miner=") {
						args = append(args, a)
					}
				}
			}
			exe := filepath.Join(dst, "Contents", "MacOS", filepath.Base(autostart.Exe()))
			start := `exec "$0" "$@"`
			argv := append([]string{exe}, args...)
			if runtime.GOOS == "darwin" {
				start = `app="$0"; exec /usr/bin/open -n "$app" --args "$@"`
				argv = append([]string{dst}, args...)
			}
			script := fmt.Sprintf(`while kill -0 %d 2>/dev/null; do sleep 0.2; done; %s`, os.Getpid(), start)
			cmd := exec.Command("/bin/sh", append([]string{"-c", script}, argv...)...)
			detach(cmd)
			if err := cmd.Start(); err != nil {
				log.Printf("relaunch: %v", err)
			} else {
				_ = cmd.Process.Release()
			}
			log.Printf("%s: moved to %s, relaunching from there", app.AppName, dst)
			shutdown()
		}
		if srv.Install.Offer {
			log.Printf("running from %s (%s): will offer to move it to Applications", srv.Install.From, srv.Install.Where)
		}
	}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		shutdown()
	}()
	fmt.Printf("\n  %s %s is running.\n\n  On this computer:  %s\n", app.AppName, version, local)
	if *host == "0.0.0.0" {
		for _, a := range app.LANAddrs() {
			fmt.Printf("  On your phone:     http://%s:%d\n", a, srv.Port)
		}
	}
	if native {
		srv.Shell = "native"
		go func() { log.Fatal(http.Serve(ln, srv.Handler())) }()
		if runNative(shellOpts{URL: local + "/?shell=" + runtime.GOOS, Origin: local, Hidden: *hidden, Srv: srv, Quit: shutdown,
			Name: app.AppName, Tagline: app.Tagline, Version: version, Dir: *dir}) {
			return
		}
		srv.Shell = "" // no native window on this machine: same app in the browser
		if !*hidden && !*noOpen {
			openBrowser(local)
		}
		select {}
	}
	if inAppBundle() {
		fmt.Printf("\n  Settings: %s\n  Running in the background. Quit from Settings → Quit %s.\n\n", *dir, app.AppName)
	} else {
		fmt.Printf("\n  Settings: %s\n  Keep this window open. Close it (or press Ctrl+C) to stop.\n  Running %s again just opens your browser to this copy.\n\n", *dir, app.AppName)
	}
	if !*noOpen && (*remote == "" || private) {
		go func() { time.Sleep(400 * time.Millisecond); openBrowser(local) }()
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
}
