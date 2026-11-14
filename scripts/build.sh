#!/usr/bin/env bash
# Cross-compile Hashwatch for every platform and zip each with the README and LICENSE.
# macOS gets a universal (arm64 + Intel) Hashwatch.app, ad-hoc signed with rcodesign
# (or codesign when run on a Mac). Set RCODESIGN=/path/to/rcodesign if it's not on PATH.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
rm -rf dist && mkdir -p dist
LDFLAGS="-s -w -X main.version=$VERSION"

targets=(
  "windows amd64 ''   windows-x64     Hashwatch.exe"
  "linux   amd64 ''   linux-x64       hashwatch"
  "linux   arm64 ''   linux-arm64     hashwatch"
  "linux   arm   7    linux-armv7     hashwatch"
)
for t in "${targets[@]}"; do
  read -r os arch arm name bin <<<"$t"; arm="${arm//\'/}"
  dir="dist/stage-$name/hashwatch-$name"; mkdir -p "$dir"
  GOOS=$os GOARCH=$arch GOARM=$arm CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$bin" ./cmd/hashwatch
  chmod 755 "$dir/$bin"
  cp README.md LICENSE "$dir/" && chmod 644 "$dir/README.md" "$dir/LICENSE"
  python3 packaging/zipdir.py "dist/hashwatch-$name.zip" "dist/stage-$name"
  echo "built dist/hashwatch-$name.zip"
done

# ---- macOS: Hashwatch.app (universal) ----
stage=dist/stage-macos; app="$stage/Hashwatch.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
for arch in arm64 amd64; do
  GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "dist/hashwatch-darwin-$arch" ./cmd/hashwatch
done
if command -v lipo >/dev/null; then
  lipo -create -output "$app/Contents/MacOS/Hashwatch" dist/hashwatch-darwin-amd64 dist/hashwatch-darwin-arm64
else
  python3 packaging/macos/lipo.py "$app/Contents/MacOS/Hashwatch" dist/hashwatch-darwin-amd64 dist/hashwatch-darwin-arm64
fi
rm dist/hashwatch-darwin-*
sed "s/__VERSION__/${VERSION#v}/g" packaging/macos/Info.plist > "$app/Contents/Info.plist"
printf 'APPL????' > "$app/Contents/PkgInfo"
cp packaging/macos/Hashwatch.icns "$app/Contents/Resources/Hashwatch.icns"
find "$app" -type d -exec chmod 755 {} + && find "$app" -type f -exec chmod 644 {} +
chmod 755 "$app/Contents/MacOS/Hashwatch"
if [[ "$(uname)" == Darwin ]] && command -v codesign >/dev/null; then
  codesign --force --deep -s - "$app"
else
  "${RCODESIGN:-rcodesign}" sign "$app"
fi
cp "packaging/macos/How to open.txt" README.md LICENSE "$stage/"
chmod 644 "$stage/How to open.txt" "$stage/README.md" "$stage/LICENSE"
if [[ "$(uname)" == Darwin ]]; then
  (cd "$stage" && ditto -c -k --keepParent Hashwatch.app ../Hashwatch-macOS.zip && zip -q ../Hashwatch-macOS.zip "How to open.txt" README.md LICENSE)
else
  python3 packaging/zipdir.py dist/Hashwatch-macOS.zip "$stage"
fi
echo "built dist/Hashwatch-macOS.zip"

# ---- macOS: Hashwatch.dmg (drag-to-Applications window), built without mounting ----
# Needs mkfs.hfsplus (hfsprogs), libdmg-hfsplus's `hfsplus` + `dmg`, and Python with ds_store + mac_alias.
if command -v "${MKFS_HFSPLUS:-mkfs.hfsplus}" >/dev/null && command -v "${HFSPLUS:-hfsplus}" >/dev/null && command -v "${DMG:-dmg}" >/dev/null; then
  "${DMG_PYTHON:-python3}" packaging/macos/make_dmg.py --app "$app" --volname Hashwatch \
    --background packaging/macos/dmg-background.tiff --icon packaging/macos/Hashwatch.icns --out dist/Hashwatch.dmg
  echo "built dist/Hashwatch.dmg"
else
  echo "skipped dist/Hashwatch.dmg (set MKFS_HFSPLUS, HFSPLUS, DMG; see README)"
fi

rm -rf dist/stage-*
(cd dist && sha256sum *.zip $(ls *.dmg 2>/dev/null) > SHA256SUMS.txt)
