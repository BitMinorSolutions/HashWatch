#!/bin/sh
# Install HashWatch as a systemd service on Raspberry Pi OS (or any systemd Linux).
# No Docker. Idempotent: a second run refreshes the binary and unit, and keeps data.
#
# One-liner (once a release host is set):
#   curl -fsSL "$RELEASE/install-pi.sh" | sudo env HASHWATCH_RELEASE_URL="$RELEASE" sh
#
# Local / test:
#   sudo ./install-pi.sh --bin ./hashwatch --update-url http://host/manifest.signed.json
#   sudo ./install-pi.sh --uninstall
#   sudo ./install-pi.sh --uninstall --purge
#
# HASHWATCH_ROOT prefixes every path (for tests, when systemd isn't available).
set -eu

NAME=hashwatch
OPT=/opt/hashwatch
DATA=/var/lib/hashwatch
UNIT=/etc/systemd/system/hashwatch.service
USER=hashwatch
CHANNEL="${HASHWATCH_CHANNEL:-stable}"
RELEASE="${HASHWATCH_RELEASE_URL:-}"
UPDATE_URL="${HASHWATCH_UPDATE_URL:-}"
BIN_SRC="${HASHWATCH_BIN:-}"
ROOT="${HASHWATCH_ROOT:-}"
UNINSTALL=0
PURGE=0
NEED_SYSTEMD=1

usage() {
	sed -n '2,16p' "$0" | sed 's/^# \?//'
	exit "${1:-0}"
}

while [ $# -gt 0 ]; do
	case "$1" in
		-h|--help) usage 0 ;;
		--uninstall) UNINSTALL=1 ;;
		--purge) PURGE=1 ;;
		--bin) BIN_SRC="$2"; shift ;;
		--update-url) UPDATE_URL="$2"; shift ;;
		--release-url) RELEASE="$2"; shift ;;
		--channel) CHANNEL="$2"; shift ;;
		--root) ROOT="$2"; shift ;;
		--no-systemd) NEED_SYSTEMD=0 ;;
		*) echo "install-pi.sh: unknown option: $1" >&2; usage 1 ;;
	esac
	shift
done

if [ -n "$ROOT" ]; then
	ROOT=$(cd "$ROOT" 2>/dev/null && pwd || echo "$ROOT")
	mkdir -p "$ROOT"
	OPT="$ROOT/opt/hashwatch"
	DATA="$ROOT/var/lib/hashwatch"
	UNIT="$ROOT/etc/systemd/system/hashwatch.service"
	NEED_SYSTEMD=0
fi

if [ -z "$UPDATE_URL" ] && [ -n "$RELEASE" ]; then
	UPDATE_URL="${RELEASE%/}/manifest.signed.json"
fi

die() { echo "install-pi.sh: $*" >&2; exit 1; }

need_root() {
	if [ -n "$ROOT" ]; then
		return 0
	fi
	if [ "$(id -u)" -ne 0 ]; then
		die "run as root (sudo). For a test prefix, pass --root DIR"
	fi
}

arch_name() {
	m=$(uname -m)
	case "$m" in
		aarch64|arm64) echo linux-arm64 ;;
		x86_64|amd64) echo linux-amd64 ;;
		armv7l|armv6l|arm) echo linux-armv7 ;;
		*) die "unsupported architecture: $m" ;;
	esac
}

fetch() {
	url="$1" dest="$2"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$dest" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$dest" "$url"
	else
		die "need curl or wget to download $url"
	fi
}

# ---- uninstall -------------------------------------------------------------
do_uninstall() {
	need_root
	if [ -z "$ROOT" ] && command -v systemctl >/dev/null 2>&1; then
		systemctl disable --now hashwatch >/dev/null 2>&1 || true
		systemctl daemon-reload >/dev/null 2>&1 || true
	fi
	rm -f "$UNIT"
	rm -rf "$OPT"
	if [ "$PURGE" -eq 1 ]; then
		rm -rf "$DATA"
		echo "Removed $OPT, $UNIT and $DATA."
	else
		echo "Removed $OPT and $UNIT. Data kept in $DATA (use --purge to delete it)."
	fi
	if [ -z "$ROOT" ] && getent passwd "$USER" >/dev/null 2>&1; then
		# Keep the user unless we also purged data, so a reinstall can reuse the uid.
		if [ "$PURGE" -eq 1 ]; then
			userdel "$USER" >/dev/null 2>&1 || true
			groupdel "$USER" >/dev/null 2>&1 || true
			echo "Removed system user $USER."
		fi
	fi
	echo "HashWatch has been uninstalled."
	exit 0
}

[ "$UNINSTALL" -eq 1 ] && do_uninstall

need_root

# ---- install ---------------------------------------------------------------
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || here="."
arch=$(arch_name)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ -n "$BIN_SRC" ]; then
	[ -f "$BIN_SRC" ] || die "binary not found: $BIN_SRC"
	cp "$BIN_SRC" "$tmp/hashwatch"
else
	[ -n "$RELEASE" ] || die "set HASHWATCH_RELEASE_URL or pass --bin /path/to/hashwatch"
	echo "Downloading hashwatch ($arch) from $RELEASE ..."
	fetch "${RELEASE%/}/hashwatch-$arch" "$tmp/hashwatch"
fi
chmod 755 "$tmp/hashwatch"

# Create the system user (skipped in --root tests).
if [ -z "$ROOT" ]; then
	if ! getent passwd "$USER" >/dev/null 2>&1; then
		if command -v useradd >/dev/null 2>&1; then
			useradd --system --home-dir "$DATA" --create-home --shell /usr/sbin/nologin --comment "HashWatch dashboard" "$USER" \
				|| useradd --system --home "$DATA" --shell /usr/sbin/nologin "$USER"
		else
			die "useradd is not available"
		fi
	fi
	id "$USER" >/dev/null
fi

mkdir -p "$OPT" "$DATA" "$(dirname "$UNIT")"
# Don't wipe an existing binary until the new one is in place (atomically).
install -m 755 "$tmp/hashwatch" "$tmp/hashwatch.installed"
if [ -f "$OPT/hashwatch" ]; then
	# Replace atomically so a running supervise can keep the old inode until restart.
	mv -f "$tmp/hashwatch.installed" "$OPT/hashwatch.new"
	mv -f "$OPT/hashwatch.new" "$OPT/hashwatch"
else
	mv -f "$tmp/hashwatch.installed" "$OPT/hashwatch"
fi
chmod 755 "$OPT/hashwatch"

# Supervise script: prefer the copy next to this installer, else write the
# embedded helper so `curl | sudo sh` still works.
write_supervise() {
	cat > "$OPT/supervise" << 'SUP'
#!/bin/sh
# Start HashWatch and roll back a failed OTA if the new binary does not
# become healthy. systemd ExecStart points here. Never talks to the miner.
set -eu

BIN="${HASHWATCH_BIN:-/opt/hashwatch/hashwatch}"
DATA="${HASHWATCH_CONFIG:-/var/lib/hashwatch}"
# Seconds to wait for the service to clear update-unconfirmed after an OTA.
HEALTH_SECS="${HASHWATCH_HEALTH_TIMEOUT:-20}"

log() { echo "hashwatch-supervise: $*" >&2; }

rollback() {
	if [ ! -x "${BIN}.bak" ]; then
		log "update was not confirmed and there is no backup"
		rm -f "$DATA/update-unconfirmed" "$DATA/update-pending.json"
		return 1
	fi
	log "new version did not become healthy; rolling back"
	mv -f "$BIN" "${BIN}.bad" 2>/dev/null || rm -f "$BIN"
	mv -f "${BIN}.bak" "$BIN"
	chmod 755 "$BIN" || true
	ver=""
	if [ -f "$DATA/update-pending.json" ]; then
		ver=$(sed -n 's/.*"to"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$DATA/update-pending.json" | head -n 1)
	fi
	if [ -n "$ver" ]; then
		printf '%s\n' "$ver" > "$DATA/update-skip"
	fi
	rm -f "$DATA/update-unconfirmed" "$DATA/update-pending.json"
	printf '%s\n' "{\"at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"ok\":false,\"rolled_back\":true,\"to\":\"$ver\",\"error\":\"new version did not become healthy; rolled back\"}" > "$DATA/update-last.json"
	return 0
}

# A previous start died before confirming — restore the backup first.
if [ -f "$DATA/update-unconfirmed" ]; then
	rollback || true
fi

if [ ! -x "$BIN" ]; then
	log "$BIN is missing or not executable"
	exit 1
fi

export HASHWATCH_SERVICE="${HASHWATCH_SERVICE:-1}"
cd "$DATA" || exit 1

start_bin() {
	# If this launch is a just-installed update, mark it unconfirmed until
	# the process itself clears the marker after /api/ping succeeds.
	if [ -f "$DATA/update-pending.json" ]; then
		touch "$DATA/update-unconfirmed"
	fi
	"$BIN" -serve -no-browser -config "$DATA" "$@" &
	CHILD=$!
}

start_bin "$@"

if [ -f "$DATA/update-unconfirmed" ]; then
	i=0
	while [ "$i" -lt "$HEALTH_SECS" ]; do
		i=$((i + 1))
		if [ ! -f "$DATA/update-unconfirmed" ]; then
			break
		fi
		if ! kill -0 "$CHILD" 2>/dev/null; then
			break
		fi
		sleep 1
	done
	if [ -f "$DATA/update-unconfirmed" ]; then
		log "health check timed out or the new process exited"
		kill "$CHILD" 2>/dev/null || true
		wait "$CHILD" 2>/dev/null || true
		if rollback; then
			# Launch the restored binary; no pending file remains.
			"$BIN" -serve -no-browser -config "$DATA" "$@" &
			CHILD=$!
		else
			exit 1
		fi
	fi
fi

# Become the service: when HashWatch exits, this script exits and systemd
# restarts it (Restart=always).
wait "$CHILD"
SUP
	chmod 755 "$OPT/supervise"
}
sup_src=""
for c in "$here/supervise.sh" "$(dirname "$here")/supervise.sh"; do
	[ -f "$c" ] && sup_src="$c" && break
done
if [ -n "$sup_src" ]; then
	install -m 755 "$sup_src" "$OPT/supervise"
else
	write_supervise
fi

# Seed config so a fresh Pi binds 0.0.0.0 (headless dashboard). Existing
# config.json is left alone.
json_str() {
	printf '"'
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
	printf '"'
}
if [ ! -f "$DATA/config.json" ]; then
	if [ -n "$UPDATE_URL" ]; then
		printf '{\n  "allow_wifi": true,\n  "update_url": %s,\n  "update_channel": %s\n}\n' \
			"$(json_str "$UPDATE_URL")" "$(json_str "$CHANNEL")" > "$DATA/config.json"
	else
		printf '{\n  "allow_wifi": true\n}\n' > "$DATA/config.json"
	fi
	chmod 600 "$DATA/config.json"
fi

# systemd unit
{
	cat <<UNIT
[Unit]
Description=HashWatch miner dashboard
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
UNIT
	if [ -z "$ROOT" ]; then
		echo "User=$USER"
		echo "Group=$USER"
	fi
	cat <<UNIT
Environment=HASHWATCH_SERVICE=1
Environment=HASHWATCH_BIN=$OPT/hashwatch
Environment=HASHWATCH_CONFIG=$DATA
UNIT
	if [ -n "$UPDATE_URL" ]; then
		# Escape any % for systemd.
		esc=$(printf '%s' "$UPDATE_URL" | sed 's/%/%%/g')
		echo "Environment=HASHWATCH_UPDATE_URL=$esc"
	fi
	cat <<UNIT
WorkingDirectory=$DATA
ExecStart=$OPT/supervise
Restart=always
RestartSec=3
TimeoutStartSec=45
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=$DATA $OPT
PrivateTmp=yes
LimitNOFILE=4096

[Install]
WantedBy=multi-user.target
UNIT
} > "$UNIT"
chmod 644 "$UNIT"

if [ -z "$ROOT" ]; then
	chown -R "$USER:$USER" "$OPT" "$DATA"
	chmod 755 "$OPT" "$DATA"
	if command -v systemctl >/dev/null 2>&1 && [ "$NEED_SYSTEMD" -eq 1 ]; then
		systemctl daemon-reload
		systemctl enable hashwatch
		if systemctl is-system-running >/dev/null 2>&1; then
			systemctl restart hashwatch
		else
			echo "systemd is not the running manager on this machine; skipped start. On a Pi, it will start on boot."
		fi
	fi
else
	# Prefix install: make the tree owned by the current user so tests can run it.
	chmod 755 "$OPT" "$DATA" || true
fi

port=48412
if [ -f "$DATA/config.json" ]; then
	p=$(sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$DATA/config.json" | head -n 1)
	[ -n "$p" ] && port=$p
fi

echo
echo "HashWatch is installed."
echo "  Binary:   $OPT/hashwatch"
echo "  Data:     $DATA"
echo "  Service:  hashwatch.service (starts on boot, restarts on failure)"
echo "  Bind:     0.0.0.0 (Allow phones on Wi-Fi is on by default on a Pi)"
if [ -n "$UPDATE_URL" ]; then
	echo "  Updates:  $UPDATE_URL  (channel: $CHANNEL)"
fi
echo
echo "  On this computer:  http://127.0.0.1:$port"

# LAN + Tailscale addresses
print_ip() {
	url="http://$1:$port"
	echo "  On your network:   $url"
}

if command -v tailscale >/dev/null 2>&1; then
	ts=$(tailscale ip -4 2>/dev/null || true)
	# MagicDNS name from status --json (no jq required).
	tsname=$(tailscale status --json 2>/dev/null | tr ',' '\n' | sed -n 's/.*"DNSName":"\([^"]*\)".*/\1/p' | head -n 1)
	tsname=${tsname%.}
	if [ -n "$tsname" ]; then
		echo "  On Tailscale:      http://$tsname:$port"
		[ -n "$ts" ] && echo "  Tailscale IP:      http://$ts:$port"
	elif [ -n "$ts" ]; then
		echo "  On Tailscale:      http://$ts:$port"
	fi
	# If tailscale serve is already proxying HTTPS, print that URL too.
	tsserve=$(tailscale serve status --json 2>/dev/null || true)
	if [ -n "$tsname" ] && printf '%s' "$tsserve" | grep -q ':443'; then
		echo "  Tailscale HTTPS:   https://$tsname"
	fi
elif [ -d /sys/class/net/tailscale0 ]; then
	ts=$(ip -4 -o addr show dev tailscale0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n 1)
	[ -n "$ts" ] && echo "  On Tailscale:      http://$ts:$port"
fi

if command -v ip >/dev/null 2>&1; then
	ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | while read -r a; do
		case "$a" in
			100.*) echo "  On Tailscale:      http://$a:$port" ;;
			*)     echo "  On your network:   http://$a:$port" ;;
		esac
	done
fi

echo
if [ -z "$ROOT" ]; then
	echo "Stop it with:    sudo systemctl stop hashwatch"
	echo "Uninstall with:  sudo $(basename "$0") --uninstall"
	echo "  (add --purge to also delete $DATA)"
else
	echo "Test prefix install (no systemd). Start it with:"
	echo "  HASHWATCH_BIN=$OPT/hashwatch HASHWATCH_CONFIG=$DATA $OPT/supervise"
	echo "Uninstall with:  $0 --root $ROOT --uninstall"
fi
echo
