# Hashwatch

A fast, good-looking, **read-only** dashboard for Avalon home miners (Nano 3 / Nano 3S and other
CGMiner-based Avalons). One small download, no installer, no Docker, no cloud account.

- Live hashrate, temperatures, power, efficiency (J/TH), fan, uptime
- 24-hour charts (hashrate 5s / 5m / 15m, chip max / avg / board / outlet temps)
- Per-chip temperature, voltage and nonce heat map
- Pools, shares, rejects, stale, hardware-error rate, best share, network drops
- Health warnings (over target temperature, high hardware-error rate, no active pool, PSU fault…)
- Controls: work speed (Low / Medium / High) and the miner light (colour, brightness, Off / Steady / Flash / Pulse / Loop)
- Multiple miners, network auto-discovery, optional password
- Works great on iPhone, Android, iPad and desktop. Add it to your home screen as an app.

Hashwatch talks to your miner **directly over your local network** (CGMiner API, TCP port 4028).
For stats it only sends read commands (`summary`, `estats`/`stats`, `pools`, `devs`, `version`).
It changes something only when you use the Controls panel and confirm. Your data never leaves your network.

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

## Run it

1. Download the zip for your computer from the Releases page and unzip it.
2. Double-click **Hashwatch** (Windows: `Hashwatch.exe`). A small window opens and your browser
   opens the dashboard at `http://localhost:8787`.
3. Enter your miner's IP address (or tap **Scan my network**). That's it.

Keep the window open while you use it; close it to stop Hashwatch.
To watch from your phone, open the address shown under **Settings → Open on your phone**
(for example `http://192.168.1.20:8787`) while on the same Wi-Fi, then **Share → Add to Home Screen**.

### macOS: "cannot be opened because the developer cannot be verified"
The app is not notarized yet. **Right-click (or Control-click) Hashwatch → Open → Open.**
You only need to do this once. On newer macOS versions you may instead need
**System Settings → Privacy & Security → Open Anyway**.

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
| `--no-browser` | | Don't open a browser on start |
| `--config DIR` | OS config dir | Where settings and chart history are stored |

Settings live in your user config folder (`~/Library/Application Support/Hashwatch`,
`%AppData%\Hashwatch`, or `~/.config/Hashwatch`). Delete it to start over.

**Security note:** by default Hashwatch is reachable by devices on your local network so your phone
can use it. Set a password in Settings if you share your network. Don't port-forward it to the internet.

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
./scripts/build.sh v0.1.0   # cross-compiles every platform into dist/
```

Layout:

```
cmd/hashwatch/      entry point (flags, browser launch)
internal/cgminer/   CGMiner TCP client, Avalon "MM ID" parser, typed control commands (workmode, ledset)
internal/sim/       simulated Avalon Nano 3 API used by tests and cmd/avalon-sim
internal/source/    data sources (direct CGMiner with controls; read-only remote dashboard for previews, no controls)
internal/app/       config, polling + history, discovery, HTTP API, optional password
internal/model/     the snapshot model shared by sources and UI
web/static/         the dashboard (vanilla JS, uPlot, Geist fonts) embedded into the binary
```

To rename the app, change `AppName` in `internal/app/config.go`.

## License

MIT. uPlot is MIT (© Leon Sorokin). Geist fonts are SIL OFL 1.1 (© Vercel).
Not affiliated with Canaan / Avalon.
