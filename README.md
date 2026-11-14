# HashWatch

*Powered by GrumpTech*

A fast, good-looking dashboard for Avalon home miners (Nano 3 / Nano 3S and other
CGMiner-based Avalons). One small download, no installer, no Docker, no cloud account.

- Live hashrate, temperatures, power, efficiency (J/TH), fan, uptime
- 24-hour charts (hashrate 5s / 5m / 15m, chip max / avg / board / outlet temps)
- Per-chip temperature, voltage and nonce heat map
- Pools, shares, rejects, stale, hardware-error rate, best share, network drops
- Health warnings (over target temperature, high hardware-error rate, no active pool, PSU fault…)
- Controls: work speed (Low / Medium / High) and the miner light (colour, brightness, Off / Steady / Flash / Pulse / Loop). Optional Advanced miner controls (off by default) can change a pool URL/worker/password and reboot the miner
- **Alerts** for block found, miner offline / back online, hashrate drops, overheating and pool
  disconnects, as desktop notifications and inside the app (with a celebration when you find a block)
- **Remote access** (optional, off by default): a private https link through Cloudflare, with a QR code for your phone
- **Start automatically** when your computer starts, quietly in the background
- Multiple miners, network auto-discovery, optional password
- Works great on iPhone, Android, iPad and desktop. Add it to your home screen as an app.

HashWatch talks to your miner **directly over your local network** (CGMiner API, TCP port 4028).
For stats it only sends read commands (`summary`, `estats`/`stats`, `pools`, `devs`, `version`).
It changes something only when you use the Controls panel and confirm, or when you turn on an
automation (the work-speed schedule or backup-pool failover). Pool URL/worker/password edits
and reboot are locked behind **Advanced miner controls** in Settings (off by default); the
server rejects those writes unless the switch is on. Your data stays on your
network, unless you turn on Remote access (see below).

## Controls

The **Controls** panel changes your miner's settings through its API. Every change asks you
to confirm, then HashWatch reads the miner back and shows the result (or the miner's error).

| Control | Command sent to the miner (port 4028) |
| --- | --- |
| Work speed Low / Medium / High | `ascset\|0,workmode,set,0` / `…,1` / `…,2` |
| Light | `ascset\|0,ledset,EFFECT-BRIGHTNESS-TEMPERATURE-R-G-B` with effect 0 Off, 1 Steady, 2 Flash, 3 Pulse (breath), 4 Loop; brightness 0–100; colour as 0–255 RGB |
| Backup pool failover (Settings, off by default) | `switchpool\|N` — switch to already-configured pool N (0–15). Never adds, removes or rewrites pool URLs or passwords. |
| Pool URL, worker and password (Advanced miner controls, off by default) | `setpool\|webuser,webpass,N,url,worker,workerpass` — N is 0–2. webuser/webpass are the miner's web login (default `admin`,`admin`). Canaan says this takes effect only after a reboot. |
| Reboot miner (Advanced miner controls, off by default) | `ascset\|0,reboot,0` |

The commands are sent as cgminer JSON, e.g. `{"command":"ascset","parameter":"0,workmode,set,1"}`.
Work speed and light follow Canaan's published API for the Avalon Nano 3S and Avalon Q.
`switchpool` is a standard cgminer command; it is **not documented** in that Canaan reference and
has not been verified on a real Nano 3, so some firmware may ignore it (the Nano already fails over
its own pool list). `setpool` and `ascset|0,reboot,0` are documented by Canaan for the Nano 3S,
Mini 3 and Avalon Q, but they have **not been tested on a real Nano 3**. HashWatch never sends
`addpool`, `removepool`, fan or standby commands. Advanced miner controls also require a confirm
dialog and the HashWatch admin password for each action; the view-only password is refused.

- Work speed takes effect within about a minute. Hashrate, power and temperatures move gradually.
- Some firmware (for example the original Nano 3) reports only the light mode, not its colour and
  brightness. HashWatch then shows the last colour you applied from HashWatch.
- The colour-temperature value is kept at what the miner reports, or 50 if it doesn't report one.
- If you set a password, you must be signed in to use the controls.
- Some community reports say older Nano 3 firmware ignores `workmode`. If the miner replies with
  an error, or the speed doesn't change, use the Avalon app instead.

## Alerts

HashWatch watches every miner on each poll (every 5 seconds; after a failed read it backs off to 15 s, then 30 s, until the miner answers again) and raises an alert when:

| Alert | When |
| --- | --- |
| Block found | the miner's **Found Blocks** counter goes up |
| Offline / back online | the miner doesn't answer for about 30 seconds, and when it answers again |
| Hashrate drop | the 5-minute hashrate stays more than 30% below its 1-hour average for 10 minutes (needs 30 minutes of history first) |
| Running hot | the max chip temperature stays above the miner's own target for 5 minutes |
| Pool disconnected | no pool is connected for two polls in a row, and when it reconnects |
| Remote link changed | the remote-access link changes |
| Daily summary | once a day at a local time you set (default 9:00 AM): uptime %, average hashrate, top temperature, best share and current odds / time-to-block over the last 24 hours |

Each alert shows up three ways: a native notification on the computer running HashWatch
(macOS Notification Center, Windows notifications, or `notify-send` on Linux), a toast in any open
HashWatch window, and the **Alerts** page, which keeps the last 300. A block find also gets a banner
on every page until you dismiss it. Turn each alert type, and desktop notifications, on or off under
**Settings → Alerts**. There's also a **Send a test alert** button there. The daily
summary time (default 9:00 AM local) is set in the same section. That briefing is
separate from the midnight "yesterday" digest under lottery extras.

### iPhone and Apple Watch (ntfy)

**Settings → iPhone & Apple Watch** can forward selected alerts to a [ntfy](https://ntfy.sh)
topic (default server `https://ntfy.sh`). Install the ntfy app on your iPhone, scan the QR code
(or open the subscribe link) and, if you set an access token, add the same token in the app.
HashWatch never puts that token, or any other credentials, in the notification. Block-found
alerts are sent at ntfy urgent/max priority; miner offline and overheat are high. Delivery is
deduped and rate-limited so a flapping miner can't spam you. Off by default.

Notes:
- Alerts are raised by the HashWatch app, so it has to be running (turn on **Start automatically**).
  Desktop notifications appear on this computer. Phone alerts use ntfy, above, and work when the
  HashWatch window isn't open.
- On Linux, desktop notifications need `notify-send` (package `libnotify-bin`) or `gdbus`.
- On macOS the first notification may ask for permission (it comes from "Script Editor"/osascript).
- Alerts only read data. They never send anything to the miner.

## Remote access (optional)

**Settings → Remote access** gives you an `https://….trycloudflare.com` link to your dashboard that
works anywhere, like on mobile data, without router changes or port forwarding.

- It's **off by default** and needs a **password**. If you don't have one, HashWatch asks you to
  create one before switching it on. Anyone who opens the link must sign in.
- Changing miners, the password, remote access or auto-start is only allowed **from your home network**,
  never through the link. Controls (work speed, light) work through the link once you're signed in.
- The first time, HashWatch downloads Cloudflare's official `cloudflared` for your system from
  [Cloudflare's GitHub releases](https://github.com/cloudflare/cloudflared/releases). It checks the
  file against the SHA-256 Cloudflare publishes for that release (and GitHub's own digest), then keeps
  it in the settings folder (`…/Hashwatch/cloudflared/`). It's 20–40 MB, so the HashWatch zips stay small.
- HashWatch watches the tunnel and restarts it if it stops or loses its connection. Remote access
  stays on across restarts.
- Scan the QR code with your phone, sign in, then in Safari tap **Share → Add to Home Screen**.

### The link can change

The free "quick tunnel" link is **new every time cloudflared starts**: when HashWatch or the computer
restarts, or after a long connection drop. To make that easy to live with:

1. Turn on **Start automatically** so HashWatch (and its link) restarts as rarely as possible.
2. When the link changes you get a **"New remote link" alert**, and the **Remote link** card on
   the Overview (shown on your home network) says **New link · re-scan**. Scan it again and replace the
   old Home Screen icon. Each browser remembers the last link it saw, so it knows when it's new.
3. For a link that **never** changes, use your own (free) Cloudflare account: in Cloudflare Zero Trust
   → Networks → Tunnels, create a tunnel, add a public hostname (e.g. `miner.example.com`) that points
   to `http://127.0.0.1:48412`, then paste the **tunnel token** and hostname under
   **Settings → Remote access → Permanent link**.

### Tailscale (if already installed)

If this computer already runs [Tailscale](https://tailscale.com), **Settings → Remote access**
and the first-run screen show its MagicDNS name or Tailscale IP as a **permanent** link. The
address stays the same after restarts. HashWatch never installs Tailscale for you. From your
home network you can optionally turn on `tailscale serve` for HTTPS. Cloudflare, above, stays
an optional alternative. The Raspberry Pi installer also prints the Tailscale URL when Tailscale
is present.

Remote access needs outbound internet access to Cloudflare (UDP/TCP port 7844). Some work or
school networks block it.

## Start automatically

**Settings → Start automatically** starts HashWatch quietly when you log in (on a Mac: in the menu bar, no window), with
no browser tab:

- **macOS**: a LaunchAgent (`~/Library/LaunchAgents/com.hashwatch.agent.plist`) that runs the binary inside
  `HashWatch.app` (`/Applications/HashWatch.app/Contents/MacOS/HashWatch -hidden …`), effective from your next login.
  Logs go to `~/Library/Application Support/Hashwatch/hashwatch.log`.
- **Windows**: a Startup entry (`HKCU\…\CurrentVersion\Run`) that runs HashWatch through a hidden launcher.
- **Linux**: a systemd user service (`~/.config/systemd/user/hashwatch.service`), or a desktop autostart
  entry if your system has no user systemd. On a headless Raspberry Pi also run
  `loginctl enable-linger $USER` so it starts without logging in.

Put HashWatch somewhere permanent first (on a Mac: drag it to Applications; elsewhere e.g. your home
folder, not Downloads), because the entry points at the file's location. Turn the switch off to remove it.

To open HashWatch, just run it again. If a copy is already running (for the same settings folder),
it opens your browser to that copy instead of starting a second one.

## Run it

### Mac
1. Download **HashWatch.dmg** from the Releases page (one app for both Apple Silicon and Intel Macs,
   macOS 11 Big Sur or newer) and double-click it.
2. In the window that opens, drag **HashWatch** onto the **Applications** folder. Then eject the
   "HashWatch" disk (in the Finder sidebar or on the desktop).
3. Open **HashWatch** from Applications (or Launchpad). It opens in its **own window** (a native
   macOS window with the dashboard, no browser needed), with a Dock icon and a **menu bar icon**.
4. Enter your miner's IP address (or tap **Scan my network**). That's it.

If you open HashWatch straight from the disk image or from Downloads, the dashboard asks
**"Move HashWatch to Applications?"**. **Move to Applications** copies it there (with `ditto`, so
permissions and the signature stay intact), restarts it from Applications and reconnects the page.
**Not now** is remembered. If your account can't write to Applications, it says so; drag it in
Finder instead.

Prefer a zip? **HashWatch-macOS.zip** has the same app: unzip it, drag **HashWatch.app** to Applications.

**The Mac app (v0.6.0):** a native window (WKWebView, dark title bar, minimum size; size and
position are remembered) with the standard menus: **About**, **Settings… ⌘,**, **Quit ⌘Q**, Edit
(copy/paste), View → **Reload ⌘R**, and Window. The **menu bar icon** (the hexagon) shows the live
hashrate and has **Open Dashboard**, **Remote access** on/off and **Quit**. **Closing the window
keeps HashWatch running in the menu bar** (the Dock icon goes away until you reopen it), so alerts
keep coming. Alerts are native macOS notifications. **Quit** (⌘Q or the menu bar) stops everything,
remote access included. Opening HashWatch again brings the window back. If the native window
can't start for some reason, HashWatch falls back to opening the dashboard in your browser.
You can still open the dashboard in any browser at `http://127.0.0.1:48412`, and on your phone.

If macOS asks whether HashWatch may find devices on your local network, click **Allow** (it needs that
to read your miner).

**"Apple could not verify HashWatch" / "cannot be opened":** HashWatch is ad-hoc signed but not
notarized by Apple yet, so the first launch is blocked once. Click **Done**, then open
**System Settings → Privacy & Security**, scroll to **Security** and click **Open Anyway** next to
HashWatch, then confirm. You only need to do this once. (The DMG and zip include these steps in
`Read me first.txt`, and `TESTING.md` lists what to try.)

### Windows and Linux
1. Download the zip for your computer from the Releases page and unzip it.
2. Double-click **HashWatch.exe** (Linux: run `./hashwatch`). A small window opens and your browser
   opens the dashboard at `http://127.0.0.1:48412`.
3. Enter your miner's IP address (or tap **Scan my network**).

Keep the window open while you use it; close it (or use **Settings → Quit HashWatch**) to stop.

To watch from your phone, open the address shown under **Settings → On your Wi-Fi**
(for example `http://192.168.1.20:48412`) while on the same Wi-Fi, then **Share → Add to Home Screen**.

### Windows: "Windows protected your PC" (SmartScreen)
Click **More info → Run anyway**. If Windows Firewall asks, allow access on **private networks**
so your phone can reach the dashboard.

### Raspberry Pi (always-on dashboard)

HashWatch can run as a systemd service on Raspberry Pi OS Lite (64-bit) — no Docker, no Umbrel.
It binds `0.0.0.0` by default on the Pi so you can open it from your LAN or Tailscale, starts on boot, and can update itself.

Once a release host is set (see **Hosting updates** below):

```sh
curl -fsSL "$RELEASE/install-pi.sh" | sudo env HASHWATCH_RELEASE_URL="$RELEASE" sh
```

The installer creates a `hashwatch` system user, puts the binary in `/opt/hashwatch`, stores data in `/var/lib/hashwatch`, and enables `hashwatch.service` (`Restart=always`). It is safe to run again (it keeps your settings). Uninstall with `sudo install-pi.sh --uninstall` (add `--purge` to also delete `/var/lib/hashwatch`).

Or, from a checkout:

```sh
make pi-bins                          # linux/arm64 + linux/amd64
sudo ./packaging/linux/install-pi.sh --bin dist/hashwatch-linux-arm64
```

On a 32-bit Pi use `make linux-armv7` and `--bin dist/hashwatch-linux-armv7`.

Updates check a signed manifest about every five minutes (or **Settings → Updates → Update now**). Each download is SHA-256 checked and the manifest is ed25519-verified with a public key baked into the binary. A failed health check rolls the previous binary back automatically. The updater never talks to the miner. Pick the **stable** or **dev** channel in Settings.

### Raspberry Pi / Linux (manual)

```sh
unzip hashwatch-linux-arm64.zip && cd hashwatch-linux-arm64
./hashwatch            # add --no-browser on a headless Pi
```
Use `hashwatch-linux-armv7` for 32-bit Raspberry Pi OS.

## Options

| Flag | Default | Meaning |
| --- | --- | --- |
| `--port` | `48412` | Web port. The port used last time is reused; if a port is taken by another program, HashWatch never uses it (it checks that its own address really reaches itself) and picks another free port automatically. |
| `--host` | `127.0.0.1` (or `0.0.0.0` if Allow phones on Wi-Fi is on) | Listen address. The Settings switch is the usual way to change this; `--host` overrides it for this run |
| `--miner 192.168.1.50` | | Add a miner without the setup screen |
| `--poll` | `5s` | How often to read each miner. Failed reads back off to 15s, then 30s, until it recovers. Gentle mode in Settings slows this to 30s |
| `--no-browser` | | Don't open a browser on start |
| `--serve` | | Server only, no app window (browser-based) |
| `--hidden` | | Start in the menu bar without opening the window (used by auto-start) |
| `--config DIR` | OS config dir | Where settings and chart history are stored. The Pi service uses `/var/lib/hashwatch`. |

Settings live in your user config folder (`~/Library/Application Support/Hashwatch`,
`%AppData%\Hashwatch`, or `~/.config/Hashwatch`). Delete it to start over.

**Security note:** by default HashWatch listens only on this computer (`127.0.0.1`). Turn on
**Allow phones on Wi-Fi** in Settings if you want devices on your local network to open it, and set a
password if you share your network. Don't port-forward it to the internet. Use Remote access instead,
which is password-protected and served over https by Cloudflare. Existing installs keep their previous
"reachable on Wi-Fi" setting until you change it.

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
./scripts/build.sh v0.6.0   # cross-compiles every platform into dist/ (+ HashWatch.dmg, SHA256SUMS.txt)
make pi-bins                 # linux/arm64 and linux/amd64 only (for the Pi and for testing here)
make pi-release VERSION=v0.6.0
# sign: go run ./cmd/hwsign keygen -out ./secrets
#       HASHWATCH_UPDATE_KEY=./secrets/ed25519.priv HASHWATCH_RELEASE_URL=$RELEASE make pi-release VERSION=v0.6.0
# bake the public key into the binary with HASHWATCH_UPDATE_PUBKEY=$(cat secrets/ed25519.pub)
```

### Hosting updates

The installer and the updater take a release URL you choose; nothing is hardcoded to a vendor account. Put `install-pi.sh`, `manifest.signed.json` and the `hashwatch-linux-*` binaries at that URL.

Options:

1. **GitHub Releases** on a repo you own (recommended to start). Free, versioned, HTTPS, and the install one-liner is just `curl …/releases/latest/download/install-pi.sh`. Attach `manifest.signed.json` and the Linux binaries to each release (or keep a dedicated “updates” release whose manifest points at versioned assets).
2. **Cloudflare R2** (or another S3-compatible bucket) plus a custom domain. Better if the binaries shouldn’t be public, or if you already use Cloudflare for the tunnel. Overwrite `manifest.signed.json` in place when you ship a channel update.
3. **GitHub Pages** or a branch of the same repo, if the binaries stay small. Fine for a personal setup; less convenient than Releases for versioned assets.

Recommendation: **GitHub Releases** until there’s a reason to hide the binaries — no extra account, an audit trail, and a simple one-liner. Keep the ed25519 private key offline (not in the repo). Bake the matching public key into Pi builds with `HASHWATCH_UPDATE_PUBKEY`. Leave `HASHWATCH_RELEASE_URL` / `HASHWATCH_UPDATE_URL` pointed at that host. This repository does not create the GitHub repo or the bucket for you.

The Mac build is a universal `HashWatch.app` (arm64 + x86_64 joined like `lipo -create`),
with `Info.plist` (`app.hashwatch`, macOS 11+, local-network ATS exception), an `.icns` icon and an ad-hoc
signature. On a Mac the script uses `lipo`, `codesign --force --deep -s -` and `ditto`; on Linux it
uses `packaging/macos/lipo.py`, [rcodesign](https://github.com/indygreg/apple-platform-rs)
(`RCODESIGN=/path/to/rcodesign`) and a zip writer that keeps exec bits. Check a built bundle with
`python3 packaging/macos/verify_sig.py HashWatch.app` (or `codesign --verify --deep --strict` on a Mac).

`HashWatch.dmg` is built without mounting anything: `mkfs.hfsplus` (hfsprogs) makes an HFS+ image,
[libdmg-hfsplus](https://github.com/mozilla/libdmg-hfsplus)'s `hfsplus` adds the app, the
`Applications` symlink, `.VolumeIcon.icns` and the background, `packaging/macos/make_dmg.py` writes the
`.DS_Store` (window size, icon positions, background alias) with the `ds_store`/`mac_alias` Python
packages, and `dmg build` compresses it to UDZO. Point the build at the tools with `MKFS_HFSPLUS`,
`HFSPLUS`, `DMG` and `DMG_PYTHON`; the DMG step is skipped if they're missing. Check the result with
`python3 packaging/macos/verify_dmg.py dist/HashWatch.dmg` (or `hdiutil verify` on a Mac). The window
background comes from `make_dmg_background.py` (1x + 2x in one HiDPI TIFF).
The native Mac shell (`cmd/hashwatch/native_darwin.go`) is pure Go: it calls Cocoa and WebKit through
the Objective-C runtime with [purego](https://github.com/ebitengine/purego), so `CGO_ENABLED=0`
cross-compiles from Linux still work. The server runs in the same process; `-serve` skips the window
(browser only), and `-hidden` starts in the menu bar (auto-start uses it).

Layout:

```
cmd/hashwatch/      entry point (flags, browser launch, native macOS shell)
packaging/macos/    Info.plist, icon (.icns + generator), Read me first.txt, lipo/verify helpers, DMG builder + background
internal/install/   "Move to Applications?" (macOS app run from Downloads or a disk image)
internal/cgminer/   CGMiner TCP client, Avalon "MM ID" parser, typed control commands (workmode, ledset, switchpool, setpool, reboot)
internal/update/    signed OTA updater for the Pi service (never talks to the miner)
packaging/linux/    install-pi.sh, systemd unit, supervise + rollback helper
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
