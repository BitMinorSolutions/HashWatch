# HashWatch V2 plan

Things deliberately left out of the v0.6 feature pack, and what v0.6 already does so they slot in later without a rewrite.

## 1. Miners in several places (multi-location)
- **Already in v0.6:** each miner has an optional `location` field and the config has a `locations` list (`internal/app/config.go`, `settings.go`). Empty means "Home". Stats, alerts and automations are already kept per miner.
- **V2:** a location picker in the header and a combined "all miners" lottery card (tickets and odds add up across miners). Remote sites connect through a small HashWatch "agent" at each site that pushes snapshots over an outbound connection, so no ports are opened at the remote site.

## 2. A permanent link
- **Already:** a named Cloudflare tunnel (token + hostname) is supported in Settings › Remote access. "New remote link" rotates only the free quick-tunnel link; for a named tunnel, "Sign out everywhere" is the kill switch.
- **V2:** one-click setup through a HashWatch relay (`<name>.hashwatch.app`) so users don't need a Cloudflare account. It needs a small hosted service, which ties into monetization below.

## 3. iPhone push notifications
- **Already:** the config has `push_targets` (kind `webpush` | `apns`). Every alert goes through `Alerts.Fire`, so adding a push sender is a single fan-out.
- **V2 path A (no App Store):** Web Push to the Home Screen web app (iOS 16.4+). It needs HTTPS, so it only works through the remote link, plus VAPID keys stored in the config.
- **V2 path B:** a native iOS companion app with APNs. It needs an Apple Developer account, which also unlocks signing and notarizing the Mac app.

## 4. Monetization (options, not decided)
- Free: everything in v0.6 (local, single site, quick-tunnel remote access).
- "HashWatch Plus" (subscription): permanent relay link, iPhone push, multiple locations, longer history (90 days+), and an email/Telegram weekly summary.
- Licence check stays offline-friendly (a signed licence file). Nothing that monitors or protects the miner is ever paywalled.

## 5. Other candidates
- Apple Developer ID signing and notarization for the Mac app (paid Apple account).
- Lottery pool-share mode for people on solo pools that report "shares toward block".
- Home Assistant / MQTT export.
- Export history (CSV).

## Removed from scope (owner decisions)
- Power cost / profit: removed. The Nano is a lottery ticket, not an income source.
- Hashrate-health extras (rolling-normal drop alert, uptime score, per-chip hints): not added.
- Auto-throttle: removed. The Nano has its own overheat protection. "Running hot" stays as an alert, based on the miner's own target (90 °C fallback).
- Auto-restart on stall: removed. HashWatch never restarts the miner.
- Backup pool failover: brought back in v0.6, **off by default**. Owner-only; never in view-only or the locked Preview. Switches among already-configured pools with `switchpool|N` after a pool has been dead or rejecting for a set time (default 5 minutes), with a cooldown, and switches back when the primary recovers. Unverified on a real Nano 3.
- Two-step sign-in (TOTP) and the signed-in device list with per-device kick: removed. Kept: password with lockout, view-only password and link, "Sign out everywhere else", "New remote link".

## Undecided (built, easy to remove)
One switch each in `internal/app/optional.go`; code is tagged `OPTIONAL:<name>`.
- `OPTIONAL:lamp` light as a status lamp · `OPTIONAL:bestflash` best-share flash · `OPTIONAL:badges` badges and streaks · `OPTIONAL:sparkline` menu bar sparkline.
