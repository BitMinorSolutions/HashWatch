# Hashwatch

A fast, good-looking, **read-only** dashboard for Avalon home miners (Nano 3 / Nano 3S and other
CGMiner-based Avalons). One small download, no installer, no Docker, no cloud account.

- Live hashrate, temperatures, power, efficiency (J/TH), fan, uptime
- 24-hour charts (hashrate 5s / 5m / 15m, chip max / avg / board / outlet temps)
- Per-chip temperature, voltage and nonce heat map
- Pools, shares, rejects, stale, hardware-error rate, best share, network drops
- Health warnings (over target temperature, high hardware-error rate, no active pool, PSU fault…)
- Controls: work speed (Low / Medium / High) and the miner light (colour, brightness, Off / Steady / Flash / Pulse / Loop)
- **Alerts** for block found, miner offline / back online, hashrate drops, overheating and pool
  disconnects, as desktop notifications and inside the app (with a celebration when you find a block)
- **Remote access** (optional, off by default): a private https link through Cloudflare, with a QR code for your phone
- **Start automatically** when your computer starts, quietly in the background
- Multiple miners, network auto-discovery, optional password
- Works great on iPhone, Android, iPad and desktop. Add it to your home screen as an app.

Hashwatch talks to your miner **directly over your local network** (CGMiner API, TCP port 4028).
For stats it only sends read commands (`summary`, `estats`/`stats`, `pools`, `devs`, `version`).
It changes something only when you use the Controls panel and confirm. Your data stays on your
network, unless you turn on Remote access (see below).

## Controls

The **Controls** panel changes your miner's settings through its API. Every change asks you
to confirm, then Hashwatch reads the miner back and shows the result (or the miner's error).

| Control | Command sent to the miner (port 4028) |
| --- | --- |
| Work speed Low / Medium / High | `ascset\|0,workmode,set,0` / `…,1` / `…,2` |
| Light | `ascset\|0,ledset,EFFECT-BRIGHTNESS-TEMPERATURE-R-G-B` with effect 0 Off, 1 Steady, 2 Flash, 3 Pulse (breath), 4 Loop; brightness 0–100; colour as 0–255 RGB |

The commands are sent as cgminer JSON, e.g. `{"command":"ascset","parameter":"0,workmode,set,1"}`.
They follow Canaan's published API for the Avalon Nano 3S and Avalon Q. Hashwatch never sends
reboot, pool, fan or standby commands.

- Work speed takes effect within about a minute. Hashrate, power and temperatures move gradually.
- Some firmware (for example the original Nano 3) reports only the light mode, not its colour and
  brightness. Hashwatch then shows the last colour you applied from Hashwatch.
- The colour-temperature value is kept at what the miner reports, or 50 if it doesn't report one.
- If you set a password, you must be signed in to use the controls.
- Some community reports say older Nano 3 firmware ignores `workmode`. If the miner replies with
  an error, or the speed doesn't change, use the Avalon app instead.

## Alerts

Hashwatch watches every miner on each poll (every 5 seconds; after a failed read it backs off to 15 s, then 30 s, until the miner answers again) and raises an alert when:

| Alert | When |
| --- | --- |
| Block found | the miner's **Found Blocks** counter goes up |
| Offline / back online | the miner doesn't answer for about 30 seconds, and when it answers again |
| Hashrate drop | the 5-minute hashrate stays more than 30% below its 1-hour average for 10 minutes (needs 30 minutes of history first) |
| Running hot | the max chip temperature stays above the miner's own target for 5 minutes |
| Pool disconnected | no pool is connected for two polls in a row, and when it reconnects |
| Remote link changed | the remote-access link changes |

Each alert shows up three ways: a native notification on the computer running Hashwatch
(macOS Notification Center, Windows notifications, or `notify-send` on Linux), a toast in any open
Hashwatch window, and the **Alerts** page, which keeps the last 300. A block find also gets a banner
on every page until you dismiss it. Turn each alert type, and desktop notifications, on or off under
**Settings → Alerts**. There's also a **Send a test alert** button there.

Notes:
- Alerts are raised by the Hashwatch app, so it has to be running (turn on **Start automatically**).
  Your phone gets them when you open Hashwatch. There's no push to a closed phone app.
- On Linux, desktop notifications need `notify-send` (package `libnotify-bin`) or `gdbus`.
- On macOS the first notification may ask for permission (it comes from "Script Editor"/osascript).
- Alerts only read data. They never send anything to the miner.

## Remote access (optional)

**Settings → Remote access** gives you an `https://….trycloudflare.com` link to your dashboard that
works anywhere, like on mobile data, without router changes or port forwarding.

- It's **off by default** and needs a **password**. If you don't have one, Hashwatch asks you to
  create one before switching it on. Anyone who opens the link must sign in.
- Changing miners, the password, remote access or auto-start is only allowed **from your home network**,
  never through the link. Controls (work speed, light) work through the link once you're signed in.
- The first time, Hashwatch downloads Cloudflare's official `cloudflared` for your system from
  [Cloudflare's GitHub releases](https://github.com/cloudflare/cloudflared/releases). It checks the
  file against the SHA-256 Cloudflare publishes for that release (and GitHub's own digest), then keeps
  it in the settings folder (`…/Hashwatch/cloudflared/`). It's 20–40 MB, so the Hashwatch zips stay small.
- Hashwatch watches the tunnel and restarts it if it stops or loses its connection. Remote access
  stays on across restarts.
- Scan the QR code with your phone, sign in, then in Safari tap **Share → Add to Home Screen**.

### The link can change

The free "quick tunnel" link is **new every time cloudflared starts**: when Hashwatch or the computer
restarts, or after a long connection drop. To make that easy to live with:

1. Turn on **Start automatically** so Hashwatch (and its link) restarts as rarely as possible.
2. When the link changes you get a **"New remote link" alert**, and the **Remote link** card on
   the Overview (shown on your home network) says **New link · re-scan**. Scan it again and replace the
   old Home Screen icon. Each browser remembers the last link it saw, so it knows when it's new.
3. For a link that **never** changes, use your own (free) Cloudflare account: in Cloudflare Zero Trust
   → Networks → Tunnels, create a tunnel, add a public hostname (e.g. `miner.example.com`) that points
   to `http://localhost:8787`, then paste the **tunnel token** and hostname under
   **Settings → Remote access → Permanent link**.

Remote access needs outbound internet access to Cloudflare (UDP/TCP port 7844). Some work or
school networks block it.

## Start automatically

**Settings → Start automatically** starts Hashwatch in the background when you log in, with
no window and no browser tab:

- **macOS**: a LaunchAgent (`~/Library/LaunchAgents/com.hashwatch.agent.plist`) that runs the binary inside
  `Hashwatch.app` (`/Applications/Hashwatch.app/Contents/MacOS/Hashwatch -serve …`), effective from your next login.
  Logs go to `~/Library/Application Support/Hashwatch/hashwatch.log`.
- **Windows**: a Startup entry (`HKCU\…\CurrentVersion\Run`) that runs Hashwatch through a hidden launcher.
- **Linux**: a systemd user service (`~/.config/systemd/user/hashwatch.service`), or a desktop autostart
  entry if your system has no user systemd. On a headless Raspberry Pi also run
  `loginctl enable-linger $USER` so it starts without logging in.

Put Hashwatch somewhere permanent first (on a Mac: drag it to Applications; elsewhere e.g. your home
folder, not Downloads), because the entry points at the file's location. Turn the switch off to remove it.

To open Hashwatch, just run it again. If a copy is already running (for the same settings folder),
it opens your browser to that copy instead of starting a second one.

## Run it

### Mac
1. Download **Hashwatch-macOS.zip** from the Releases page (one app for both Apple Silicon and Intel Macs,
   macOS 11 Big Sur or newer) and double-click it to unzip.
2. Drag **Hashwatch.app** into **Applications**.
3. Double-click **Hashwatch**. It runs quietly in the background (no Terminal window, no Dock icon)
   and your browser opens the dashboard at `http://localhost:8787`.
4. Enter your miner's IP address (or tap **Scan my network**). That's it.

To open the dashboard again later, double-click Hashwatch again: it reopens your browser to the copy
that's already running. To stop it, use **Settings → Quit Hashwatch**.
If macOS asks whether Hashwatch may find devices on your local network, click **Allow** (it needs that
to read your miner).

**"Apple could not verify Hashwatch" / "cannot be opened":** Hashwatch is ad-hoc signed but not
notarized by Apple yet, so the first launch is blocked once. Click **Done**, then open
**System Settings → Privacy & Security**, scroll to **Security** and click **Open Anyway** next to
Hashwatch, then confirm. You only need to do this once. (The zip includes these steps in
`How to open.txt`.)

### Windows and Linux
1. Download the zip for your computer from the Releases page and unzip it.
2. Double-click **Hashwatch.exe** (Linux: run `./hashwatch`). A small window opens and your browser
   opens the dashboard at `http://localhost:8787`.
3. Enter your miner's IP address (or tap **Scan my network**).

Keep the window open while you use it; close it (or use **Settings → Quit Hashwatch**) to stop.

To watch from your phone, open the address shown under **Settings → On your Wi-Fi**
(for example `http://192.168.1.20:8787`) while on the same Wi-Fi, then **Share → Add to Home Screen**.

### Windows: "Windows protected your PC" (SmartScreen)
Click **More info → Run anyway**. If Windows Firewall asks, allow access on **private networks**
so your phone can reach the dashboard.

### Raspberry Pi / Linux
```sh
unzip hashwatch-linux-arm64.zip && cd hashwatch-linux-arm64
./hashwatch            # add --no-browser on a headless Pi
```
Use `hashwatch-linux-armv7` for 32-bit Raspberry Pi OS.

## Options

| Flag | Default | Meaning |
| --- | --- | --- |
| `--port` | `8787` | Web port (the next free port is used if busy) |
| `--host` | `0.0.0.0` | Listen address. Use `127.0.0.1` to allow this computer only |
| `--miner 192.168.1.50` | | Add a miner without the setup screen |
| `--poll` | `5s` | How often to read each miner. Failed reads back off to 15s, then 30s, until it recovers |
| `--no-browser` | | Don't open a browser on start |
| `--serve` | | Run the server in this process (used inside Hashwatch.app and by auto-start) |
| `--config DIR` | OS config dir | Where settings and chart history are stored |

Settings live in your user config folder (`~/Library/Application Support/Hashwatch`,
`%AppData%\Hashwatch`, or `~/.config/Hashwatch`). Delete it to start over.

**Security note:** by default Hashwatch is reachable by devices on your local network so your phone
can use it. Set a password in Settings if you share your network. Don't port-forward it to the internet.
Use Remote access instead, which is password-protected and served over https by Cloudflare.

### The miner isn't found
- Make sure the computer and the miner are on the same network.
- The CGMiner API must be reachable on port 4028 (it is on stock Avalon firmware).
- Scanning covers your computer's own /24 subnet. On other subnets, enter the IP manually.

## Build from source

Requires Go 1.24+. No other dependencies (the frontend and uPlot are embedded).

```sh
go test ./...
go run ./cmd/hashwatch
go run ./cmd/avalon-sim &                      # simulated Nano 3 on 127.0.0.1:4028
go run ./cmd/hashwatch --miner 127.0.0.1:4028  # try everything, controls included, without hardware
go run ./cmd/avalon-sim -admin 127.0.0.1:4029 &  # test knobs: curl 127.0.0.1:4029/block (found blocks +1),
                                                #   /offline?on=1, /pool?down=1, /hash?f=0.5, /heat?c=6
./scripts/build.sh v0.4.1   # cross-compiles every platform into dist/ (+ SHA256SUMS.txt)
```

The Mac build is a universal `Hashwatch.app` (arm64 + x86_64 joined like `lipo -create`),
with `Info.plist` (`app.hashwatch`, `LSUIElement`, macOS 11+), an `.icns` icon and an ad-hoc
signature. On a Mac the script uses `lipo`, `codesign --force --deep -s -` and `ditto`; on Linux it
uses `packaging/macos/lipo.py`, [rcodesign](https://github.com/indygreg/apple-platform-rs)
(`RCODESIGN=/path/to/rcodesign`) and a zip writer that keeps exec bits. Check a built bundle with
`python3 packaging/macos/verify_sig.py Hashwatch.app` (or `codesign --verify --deep --strict` on a Mac).
Inside the app, the visible executable is a launcher: it starts the server (`-serve`) in the background
and exits, so the app never holds a Terminal and opening it again just reopens the browser.

Layout:

```
cmd/hashwatch/      entry point (flags, browser launch, macOS app launcher)
packaging/macos/    Info.plist, icon (.icns + generator), How to open.txt, lipo/verify helpers
internal/cgminer/   CGMiner TCP client, Avalon "MM ID" parser, typed control commands (workmode, ledset)
internal/sim/       simulated Avalon Nano 3 API used by tests and cmd/avalon-sim
internal/source/    data sources (direct CGMiner with controls; read-only remote dashboard for previews, no controls)
internal/app/       config, polling + history, discovery, HTTP API, password, alerts, single-instance
internal/tunnel/    remote access: cloudflared download + checksum check, run, watch, restart
internal/qr/        small QR code encoder (SVG), so the link QR is made locally
internal/notify/    native desktop notifications (macOS / Windows / Linux)
internal/autostart/ start at login (LaunchAgent / Run key / systemd user service)
internal/model/     the snapshot model shared by sources and UI
web/static/         the dashboard (vanilla JS, uPlot, Geist fonts) embedded into the binary
```

To rename the app, change `AppName` in `internal/app/config.go`.

## License

MIT. uPlot is MIT (© Leon Sorokin). Geist fonts are SIL OFL 1.1 (© Vercel). Lucide icons are ISC.
cloudflared (downloaded only if you turn on Remote access) is Apache 2.0 (© Cloudflare) and isn't bundled.
Not affiliated with Canaan / Avalon.
