# HashWatch

HashWatch is a local dashboard for a Canaan Avalon Nano 3 (and Nano 3S / Mini 3 / Avalon Q) Bitcoin miner. It runs on your Mac or a Raspberry Pi, reads the miner over its LAN API (port 4028), and shows hashrate, temperature, pool status, lottery-style odds, and alerts. Optional remote access uses Tailscale (if you already have it) or a Cloudflare quick tunnel. Phone alerts can go to the [ntfy](https://ntfy.sh) app on an iPhone or Apple Watch.

It never ports-forward your miner, and it never talks to the miner unless you confirm a change in the dashboard.

## Mac

Download [HashWatch.dmg](https://github.com/BitMinorSolutions/HashWatch/releases/latest/download/HashWatch.dmg) (or the [zip](https://github.com/BitMinorSolutions/HashWatch/releases/latest/download/HashWatch-macOS.zip)).

1. Open the disk image and drag **HashWatch** into **Applications**.
2. Eject the image, then open HashWatch from Applications.

**First launch.** This build is not signed or notarized by Apple, so macOS will say it cannot verify HashWatch. In Finder, **Control-click** (or right-click) HashWatch, choose **Open**, then click **Open** in the dialog. You only need to do this once. If you already double-clicked it, click **Done**, then System Settings → Privacy & Security → **Open Anyway**.

When macOS asks to allow local network access, click Allow so HashWatch can reach the miner.

A zip of the same app is attached to the [latest release](https://github.com/BitMinorSolutions/HashWatch/releases/latest) if you prefer not to use a disk image.

## Raspberry Pi

On Raspberry Pi OS (64-bit) or any systemd Linux box, this installs HashWatch as a service that starts on boot and can update itself:

```sh
curl -fsSL https://github.com/BitMinorSolutions/HashWatch/releases/latest/download/install-pi.sh | sudo sh
```

It creates a `hashwatch` system user, installs to `/opt/hashwatch`, stores settings in `/var/lib/hashwatch`, and binds on all interfaces so you can open it from your LAN or Tailscale. Signed OTA updates are checked about every five minutes (stable channel) from this GitHub Releases page. The updater never talks to the miner.

Uninstall with:

```sh
curl -fsSL https://github.com/BitMinorSolutions/HashWatch/releases/latest/download/install-pi.sh | sudo sh -s -- --uninstall
```

Add `--purge` to also delete `/var/lib/hashwatch`.

## Safety

- Do not port-forward the miner’s API (4028) or the HashWatch port to the internet.
- If phones on your Wi-Fi can open the dashboard, set a HashWatch password. Until you do, only the computer running HashWatch can change settings or the miner.
- Work speed, the miner light, pool changes and reboot are confirm-and-password actions. Advanced pool/reboot controls are off until you turn them on.
- HashWatch does not install Tailscale or cloudflared unless you choose those optional remote-access features (cloudflared is downloaded only if you turn Remote access on).
- Release binaries are SHA-256 checksummed. Pi / Linux service updates are also ed25519-signed; the public key is baked into the binary.

## Build from source

Go 1.22+ (this tree uses the version in `go.mod`):

```sh
go test ./...
go build -trimpath -ldflags "-s -w -X main.version=0.8.2" -o hashwatch ./cmd/hashwatch
```

The installer, Linux binaries and signed update manifest used by the Pi one-liner are attached to each [GitHub release](https://github.com/BitMinorSolutions/HashWatch/releases). Keep the ed25519 private signing key offline (never in the repo).

## License

MIT. See [LICENSE](LICENSE).
