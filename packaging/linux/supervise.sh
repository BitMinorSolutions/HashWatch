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
