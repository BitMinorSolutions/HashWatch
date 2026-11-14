#!/usr/bin/env bash
# Stage Pi / Linux service release files under dist/pi.
# Does not sign the manifest unless HASHWATCH_UPDATE_KEY (path to ed25519.priv) is set.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
mkdir -p dist/pi
for a in linux-amd64 linux-arm64; do
  src="dist/hashwatch-$a"
  if [[ ! -x "$src" ]]; then
    echo "missing $src — run make pi-bins first" >&2
    exit 1
  fi
  cp "$src" "dist/pi/hashwatch-$a"
  chmod 755 "dist/pi/hashwatch-$a"
done
cp packaging/linux/install-pi.sh packaging/linux/supervise.sh dist/pi/
chmod 755 dist/pi/install-pi.sh dist/pi/supervise.sh

# Manifest with file://-style relative URLs is not used; we write http-relative
# names so a release host can sit the files next to the manifest.
python3 - "$VERSION" << 'PY'
import hashlib, json, os, sys, datetime
version = sys.argv[1]
builds = {}
for name in ("linux-amd64", "linux-arm64"):
    path = f"dist/pi/hashwatch-{name}"
    data = open(path, "rb").read()
    builds[name] = {
        "url": f"./hashwatch-{name}",  # release script / host should rewrite to an absolute URL
        "sha256": hashlib.sha256(data).hexdigest(),
        "size": len(data),
    }
man = {
    "schema": 1,
    "channels": {
        "stable": {"version": version, "published": datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"), "builds": builds},
        "dev": {"version": version, "published": datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"), "builds": builds},
    },
}
open("dist/pi/manifest.json", "w").write(json.dumps(man, indent=2) + "\n")
print("wrote dist/pi/manifest.json")
PY

if [[ -n "${HASHWATCH_UPDATE_KEY:-}" ]]; then
  if [[ ! -x dist/hwsign ]]; then
    go build -trimpath -o dist/hwsign ./cmd/hwsign
  fi
  # Rewrite build URLs to absolute if HASHWATCH_RELEASE_URL is set.
  if [[ -n "${HASHWATCH_RELEASE_URL:-}" ]]; then
    python3 - "$HASHWATCH_RELEASE_URL" << 'PY'
import json, sys
base = sys.argv[1].rstrip("/")
man = json.load(open("dist/pi/manifest.json"))
for ch in man["channels"].values():
    for b in ch["builds"].values():
        if b["url"].startswith("./"):
            b["url"] = base + "/" + b["url"][2:]
json.dump(man, open("dist/pi/manifest.json", "w"), indent=2)
open("dist/pi/manifest.json", "a").write("\n")
PY
  fi
  ./dist/hwsign sign -key "$HASHWATCH_UPDATE_KEY" -in dist/pi/manifest.json -out dist/pi/manifest.signed.json
  echo "wrote dist/pi/manifest.signed.json"
else
  echo "note: HASHWATCH_UPDATE_KEY not set; dist/pi/manifest.json is unsigned."
  echo "  Generate a key with: go run ./cmd/hwsign keygen -out ./secrets"
  echo "  Sign with: HASHWATCH_UPDATE_KEY=./secrets/ed25519.priv $0 $VERSION"
fi
echo "staged dist/pi/"
ls -l dist/pi
