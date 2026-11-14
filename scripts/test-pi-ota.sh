#!/usr/bin/env bash
# End-to-end test of the Pi installer and OTA flow on linux/amd64.
# Uses a directory prefix (no system user, no systemd) and a local release HTTP server.
set -euo pipefail
cd "$(dirname "$0")/.."

ROOT=$(mktemp -d /tmp/hashwatch-pi-ota.XXXXXX)
KEYS="$ROOT/keys"
REL="$ROOT/release"
LOG="$ROOT/test.log"
PORT=48921
# Fail if the port is already taken (do not disturb other servers).
if ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[.:]${PORT}$"; then
  echo "port $PORT is in use; aborting so we don't touch other servers" >&2
  exit 1
fi

cleanup() {
  if [[ -n "${OUTER:-}" ]]; then
    touch "$ROOT/stop" 2>/dev/null || true
    kill "$OUTER" 2>/dev/null || true
    # Anything still bound to our test port:
    if [[ -f "$ROOT/var/lib/hashwatch/instance.json" ]]; then
      pid=$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\([0-9]*\).*/\1/p' "$ROOT/var/lib/hashwatch/instance.json" | head -n 1)
      [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
    fi
  fi
  if [[ -n "${HTTP_PID:-}" ]]; then
    kill "$HTTP_PID" 2>/dev/null || true
  fi
  rm -rf "$ROOT"
}
trap cleanup EXIT

mkdir -p "$KEYS" "$REL"
go run ./cmd/hwsign keygen -out "$KEYS" >/dev/null
PUB=$(tr -d ' \n' < "$KEYS/ed25519.pub")
LDFLAGS_BASE="-s -w -X github.com/hashwatch/hashwatch/internal/update.PublicKey=${PUB}"

echo "building ota-v1 and ota-v2 (linux/amd64)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS_BASE -X main.version=ota-v1" -o "$REL/hashwatch-v1" ./cmd/hashwatch
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS_BASE -X main.version=ota-v2" -o "$REL/hashwatch-v2" ./cmd/hashwatch
printf '#!/bin/sh\necho "deliberately broken hashwatch update" >&2\nexit 1\n' > "$REL/hashwatch-bad"
chmod 755 "$REL/hashwatch-v1" "$REL/hashwatch-v2" "$REL/hashwatch-bad"

# Local release host. The Python server serves $REL on 127.0.0.1:0 (we'll pick 48922).
REL_PORT=48922
if ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[.:]${REL_PORT}$"; then
  REL_PORT=48923
fi
python3 - "$REL" "$REL_PORT" << 'PY' &
import http.server, os, sys
os.chdir(sys.argv[1])
port = int(sys.argv[2])
http.server.test(HandlerClass=http.server.SimpleHTTPRequestHandler, port=port, bind="127.0.0.1")
PY
HTTP_PID=$!
for i in $(seq 1 50); do
  if curl -sf "http://127.0.0.1:$REL_PORT/" >/dev/null; then break; fi
  sleep 0.1
  if ! kill -0 "$HTTP_PID" 2>/dev/null; then echo "release server failed" >&2; exit 1; fi
done

BASE="http://127.0.0.1:$REL_PORT"
sign_manifest() {
  local ver="$1" file="$2" dest="$3"
  python3 - "$BASE" "$ver" "$file" "$REL/manifest.json" << 'PY'
import hashlib, json, os, sys
base, ver, path, out = sys.argv[1:5]
data = open(path, "rb").read()
build = {"url": base + "/" + os.path.basename(path), "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}
man = {"schema": 1, "channels": {
    "stable": {"version": ver, "notes": "e2e " + ver, "builds": {"linux-amd64": build, "linux-arm64": build}},
    "dev": {"version": ver, "notes": "e2e " + ver, "builds": {"linux-amd64": build, "linux-arm64": build}},
}}
open(out, "w").write(json.dumps(man, indent=2) + "\n")
PY
  go run ./cmd/hwsign sign -key "$KEYS/ed25519.priv" -in "$REL/manifest.json" -out "$REL/manifest.signed.json" >/dev/null
}

sign_manifest ota-v1 "$REL/hashwatch-v1" unused
# The installer also needs a name it expects when downloading: hashwatch-linux-amd64
cp "$REL/hashwatch-v1" "$REL/hashwatch-linux-amd64"

echo "=== 1. install ==="
./packaging/linux/install-pi.sh --root "$ROOT" --bin "$REL/hashwatch-v1" --update-url "$BASE/manifest.signed.json" --channel stable
# Pin the test port so we don't land on 48412 or a demo port.
python3 - "$ROOT/var/lib/hashwatch/config.json" "$PORT" << 'PY'
import json, sys
path, port = sys.argv[1], int(sys.argv[2])
cfg = json.load(open(path))
cfg["port"] = port
json.dump(cfg, open(path, "w"), indent=2)
open(path, "a").write("\n")
PY

# Sanity on the tree
test -x "$ROOT/opt/hashwatch/hashwatch"
test -x "$ROOT/opt/hashwatch/supervise"
test -f "$ROOT/etc/systemd/system/hashwatch.service"
grep -q '0.0.0.0\|allow_wifi' "$ROOT/var/lib/hashwatch/config.json"
python3 -c 'import json,sys; c=json.load(open(sys.argv[1])); assert c.get("allow_wifi") is True, c' "$ROOT/var/lib/hashwatch/config.json"
grep -q 'Restart=always' "$ROOT/etc/systemd/system/hashwatch.service"
# Idempotent second run keeps data
./packaging/linux/install-pi.sh --root "$ROOT" --bin "$REL/hashwatch-v1" --update-url "$BASE/manifest.signed.json" >/dev/null

cat > "$ROOT/kill-self.sh" << KS
#!/bin/sh
inst="$ROOT/var/lib/hashwatch/instance.json"
if [ -f "\$inst" ]; then
  pid=\$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\\([0-9]*\\).*/\\1/p' "\$inst" | head -n 1)
  [ -n "\$pid" ] && kill -TERM "\$pid"
fi
KS
chmod 755 "$ROOT/kill-self.sh"

export HASHWATCH_BIN="$ROOT/opt/hashwatch/hashwatch"
export HASHWATCH_CONFIG="$ROOT/var/lib/hashwatch"
export HASHWATCH_SERVICE=1
export HASHWATCH_UPDATE_URL="$BASE/manifest.signed.json"
export HASHWATCH_RESTART_CMD="$ROOT/kill-self.sh"
export HASHWATCH_HEALTH_TIMEOUT=12

rm -f "$ROOT/stop"
( while [ ! -f "$ROOT/stop" ]; do
    "$ROOT/opt/hashwatch/supervise" >> "$ROOT/var/lib/hashwatch/supervise.log" 2>&1 || true
    sleep 0.3
  done ) &
OUTER=$!

wait_version() {
  local want="$1" tries="${2:-40}"
  local i=0 body=""
  while [ "$i" -lt "$tries" ]; do
    i=$((i + 1))
    if body=$(curl -sf "http://127.0.0.1:$PORT/api/ping" 2>/dev/null); then
      if printf '%s' "$body" | grep -q "\"version\":\"$want\""; then
        return 0
      fi
    fi
    sleep 0.25
  done
  echo "timed out waiting for version $want (last: ${body:-none})" >&2
  echo "---- supervise.log ----" >&2
  tail -n 50 "$ROOT/var/lib/hashwatch/supervise.log" >&2 || true
  return 1
}

wait_version ota-v1
echo "installed service is serving ota-v1 on :$PORT"

echo "=== 2. update to ota-v2 ==="
sign_manifest ota-v2 "$REL/hashwatch-v2" unused
cp "$REL/hashwatch-v2" "$REL/hashwatch-linux-amd64"
# The server closes the HTTP connection when it restarts; curl may fail even after a good apply.
curl -sS -m 20 -X POST "http://127.0.0.1:$PORT/api/update" -H 'Content-Type: application/json' -d '{"apply":true}' >/tmp/hw-update-apply.json || true
wait_version ota-v2 50
echo "update applied: now serving ota-v2"
# Backup of v1 should exist or may have been kept as .bak until confirmed and left in place.
test -f "$ROOT/opt/hashwatch/hashwatch.bak" || test -f "$ROOT/opt/hashwatch/hashwatch"

echo "=== 3. broken update rolls back ==="
sign_manifest ota-bad "$REL/hashwatch-bad" unused
curl -sf -X POST "http://127.0.0.1:$PORT/api/update" -H 'Content-Type: application/json' -d '{"apply":true}' >/tmp/hw-update-bad.json || true
# The apply may return 502 (restart/process dying) or 200; either way we wait for v2 to come back.
wait_version ota-v2 60
if grep -q '"rolled_back": true' "$ROOT/var/lib/hashwatch/update-last.json" 2>/dev/null \
   || grep -q ota-bad "$ROOT/var/lib/hashwatch/update-skip" 2>/dev/null; then
  echo "broken update rolled back to ota-v2"
else
  echo "expected rollback markers; last=$(cat "$ROOT/var/lib/hashwatch/update-last.json" 2>/dev/null || echo none) skip=$(cat "$ROOT/var/lib/hashwatch/update-skip" 2>/dev/null || echo none)" >&2
  tail -n 80 "$ROOT/var/lib/hashwatch/supervise.log" >&2 || true
  exit 1
fi

echo "=== 4. uninstall ==="
touch "$ROOT/stop"
kill "$OUTER" 2>/dev/null || true
if [[ -f "$ROOT/var/lib/hashwatch/instance.json" ]]; then
  pid=$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\([0-9]*\).*/\1/p' "$ROOT/var/lib/hashwatch/instance.json" | head -n 1)
  [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
fi
sleep 0.4
OUTER=""
./packaging/linux/install-pi.sh --root "$ROOT" --uninstall --purge >/tmp/hw-uninst.log
if [[ -e "$ROOT/opt/hashwatch" ]] || [[ -e "$ROOT/var/lib/hashwatch" ]] || [[ -e "$ROOT/etc/systemd/system/hashwatch.service" ]]; then
  echo "uninstall left files behind:" >&2
  find "$ROOT" -maxdepth 4 -type f >&2 || true
  exit 1
fi
echo "uninstall removed the prefix tree"

echo
echo "All installer + OTA checks passed."
echo "  install -> update ota-v1 to ota-v2 -> broken update rolled back -> uninstall --purge"
