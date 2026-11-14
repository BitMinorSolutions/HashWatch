# HashWatch — common build targets. See scripts/build.sh for a full release
# (every OS, the Mac app and optional DMG).
VERSION    ?= 0.7.0
UPDATE_KEY ?= $(HASHWATCH_UPDATE_PUBKEY)
LDFLAGS     = -s -w -X main.version=$(VERSION)
ifneq ($(UPDATE_KEY),)
LDFLAGS    += -X github.com/hashwatch/hashwatch/internal/update.PublicKey=$(UPDATE_KEY)
endif

.PHONY: test linux-amd64 linux-arm64 linux-armv7 pi-bins pi-release hwsign clean

test:
	go test ./...

linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/hashwatch-linux-amd64 ./cmd/hashwatch

linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/hashwatch-linux-arm64 ./cmd/hashwatch

linux-armv7:
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/hashwatch-linux-armv7 ./cmd/hashwatch

# Cross-compile the Pi (arm64) and the amd64 binary used to test the installer here.
pi-bins: linux-arm64 linux-amd64

hwsign:
	go build -trimpath -o dist/hwsign ./cmd/hwsign

# Stage a Pi release directory: both Linux binaries, the installer, and a
# manifest.json ready to sign with: dist/hwsign sign -key KEY -in dist/pi/manifest.json -out dist/pi/manifest.signed.json
pi-release: pi-bins hwsign
	./scripts/build-pi.sh $(VERSION)

clean:
	rm -rf dist/hashwatch-linux-* dist/hwsign dist/pi
