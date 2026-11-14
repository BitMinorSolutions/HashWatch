#!/usr/bin/env bash
# Cross-compile Hashwatch for every platform and zip each with the README and LICENSE.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
rm -rf dist && mkdir -p dist
targets=(
  "windows amd64 ''   windows-x64     Hashwatch.exe"
  "darwin  arm64 ''   macos-arm64     Hashwatch"
  "darwin  amd64 ''   macos-intel     Hashwatch"
  "linux   amd64 ''   linux-x64       hashwatch"
  "linux   arm64 ''   linux-arm64     hashwatch"
  "linux   arm   7    linux-armv7     hashwatch"
)
for t in "${targets[@]}"; do
  read -r os arch arm name bin <<<"$t"; arm="${arm//\'/}"
  dir="dist/hashwatch-$name"; mkdir -p "$dir"
  GOOS=$os GOARCH=$arch GOARM=$arm CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$dir/$bin" ./cmd/hashwatch
  cp README.md LICENSE "$dir/"
  python3 - "$name" <<'PY'
import os, sys, zipfile
name = "hashwatch-" + sys.argv[1]
with zipfile.ZipFile(f"dist/{name}.zip", "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for f in sorted(os.listdir(f"dist/{name}")):
        path = f"dist/{name}/{f}"
        info = zipfile.ZipInfo(f"{name}/{f}")
        info.external_attr = (0o100755 if os.access(path, os.X_OK) and not f.endswith((".md", "LICENSE")) else 0o100644) << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        info.create_system = 3
        z.writestr(info, open(path, "rb").read())
PY
  echo "built dist/hashwatch-$name.zip"
done
(cd dist && sha256sum *.zip > SHA256SUMS.txt)
