// Package update implements signed, self-hosted OTA updates for a HashWatch
// service (the Raspberry Pi dashboard). It never talks to a miner.
package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultReleaseURL is the public GitHub Releases folder HashWatch checks for
// updates (stable channel). A directory URL is normalized to manifest.signed.json.
const DefaultReleaseURL = "https://github.com/BitMinorSolutions/HashWatch/releases/latest/download"

const ManifestName = "manifest.signed.json"

// PublicKey is the hex-encoded ed25519 public key used to verify release
// manifests. The default is the HashWatch v0.7+ release key. Builds may
// override it with
//
//	-ldflags "-X github.com/hashwatch/hashwatch/internal/update.PublicKey=..."
//
// A build with an empty key will not apply updates.
var PublicKey = "a34f05937c8dfe6bdfb925adc3cfe3ecfc8aed260441e74b8e4bcb484c8c468c"

// ManifestURL turns a release folder or a direct manifest URL into the
// signed-manifest URL the updater fetches.
func ManifestURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	u = strings.TrimRight(u, "/")
	low := strings.ToLower(u)
	if strings.HasSuffix(low, "/"+ManifestName) || strings.HasSuffix(low, ManifestName) {
		return u
	}
	return u + "/" + ManifestName
}

// Envelope is a signed release document: Manifest is the exact JSON that was
// signed, Signature is a standard-base64 ed25519 signature of those bytes.
type Envelope struct {
	V         int    `json:"v"`
	Manifest  string `json:"manifest"`
	Signature string `json:"signature"`
}

// Manifest lists available builds on one or more channels.
type Manifest struct {
	Schema   int                `json:"schema"`
	Channels map[string]Release `json:"channels"`
}

// Release is one published version on a channel.
type Release struct {
	Version   string           `json:"version"`
	Notes     string           `json:"notes,omitempty"`
	Published string           `json:"published,omitempty"`
	Builds    map[string]Build `json:"builds"`
}

// Build is one platform's binary.
type Build struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

// ParsePublicKey decodes a 32-byte ed25519 public key from hex (or a raw 64-char string).
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("no update signing key in this build")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("update public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("update public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}

// OpenEnvelope verifies the signature and returns the parsed manifest.
func OpenEnvelope(raw []byte, pub ed25519.PublicKey) (*Manifest, []byte, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, fmt.Errorf("update manifest: %w", err)
	}
	if env.V != 1 {
		return nil, nil, fmt.Errorf("update manifest: unsupported version %d", env.V)
	}
	if env.Manifest == "" || env.Signature == "" {
		return nil, nil, fmt.Errorf("update manifest: missing manifest or signature")
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return nil, nil, fmt.Errorf("update signature: %w", err)
	}
	payload := []byte(env.Manifest)
	if l := len(payload); l == 0 || l > 1<<20 {
		return nil, nil, fmt.Errorf("update manifest: unreasonable size")
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, payload, sig) {
		return nil, nil, fmt.Errorf("update manifest: signature is not valid")
	}
	var man Manifest
	if err := json.Unmarshal(payload, &man); err != nil {
		return nil, nil, fmt.Errorf("update manifest: %w", err)
	}
	if man.Channels == nil {
		return nil, nil, fmt.Errorf("update manifest: no channels")
	}
	return &man, payload, nil
}

// SignEnvelope signs payload (the exact manifest JSON) and returns the envelope JSON.
func SignEnvelope(payload []byte, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("ed25519 private key is the wrong size")
	}
	sig := ed25519.Sign(key, payload)
	env := Envelope{V: 1, Manifest: string(payload), Signature: base64.StdEncoding.EncodeToString(sig)}
	return json.MarshalIndent(env, "", "  ")
}

// Platform is the build map key for this binary (linux-arm64, linux-amd64, linux-armv7).
func Platform(goos, goarch string) string {
	if goos == "linux" && goarch == "arm" {
		return "linux-armv7"
	}
	return goos + "-" + goarch
}

func (m *Manifest) Release(channel string) (Release, error) {
	ch := NormalizeChannel(channel)
	rel, ok := m.Channels[ch]
	if !ok {
		return Release{}, fmt.Errorf("update manifest has no %q channel", ch)
	}
	if strings.TrimSpace(rel.Version) == "" {
		return Release{}, fmt.Errorf("update manifest: %s is missing a version", ch)
	}
	return rel, nil
}

func (r Release) Build(platform string) (Build, error) {
	b, ok := r.Builds[platform]
	if !ok {
		return Build{}, fmt.Errorf("no %s build in this release", platform)
	}
	if err := checkBuild(b); err != nil {
		return Build{}, err
	}
	return b, nil
}

func checkBuild(b Build) error {
	if !validUpdateURL(b.URL) {
		return fmt.Errorf("build URL must be http(s)")
	}
	sum := strings.ToLower(strings.TrimSpace(b.SHA256))
	if len(sum) != 64 {
		return fmt.Errorf("build is missing a SHA-256")
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return fmt.Errorf("build SHA-256 is not hex")
	}
	return nil
}

func validUpdateURL(u string) bool {
	u = strings.ToLower(u)
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// NormalizeChannel returns "stable" or "dev".
func NormalizeChannel(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "dev") {
		return "dev"
	}
	return "stable"
}

// Newer reports whether candidate is a newer version than current.
// Both may have an optional "v" prefix. Unparsable values are treated as
// different (so a dev build tagged "ota-v2" can replace "ota-v1").
func Newer(current, candidate string) bool {
	c, cok := parseVer(current)
	d, dok := parseVer(candidate)
	if cok && dok {
		for i := 0; i < 3; i++ {
			if d[i] != c[i] {
				return d[i] > c[i]
			}
		}
		return false
	}
	return strings.TrimPrefix(strings.TrimSpace(candidate), "v") != strings.TrimPrefix(strings.TrimSpace(current), "v")
}

func parseVer(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		if p == "" {
			return [3]int{}, false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return [3]int{}, false
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out, true
}
