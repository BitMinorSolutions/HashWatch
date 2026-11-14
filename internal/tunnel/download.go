package tunnel

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// ReleaseAPI is Cloudflare's official cloudflared release feed on GitHub.
var ReleaseAPI = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"

// AssetName is the official release file for this OS / CPU.
func AssetName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64", "linux/386":
		return "cloudflared-linux-" + goarch, nil
	case "linux/arm":
		return "cloudflared-linux-armhf", nil
	case "windows/amd64", "windows/386":
		return "cloudflared-windows-" + goarch + ".exe", nil
	case "darwin/amd64", "darwin/arm64":
		return "cloudflared-darwin-" + goarch + ".tgz", nil
	}
	return "", fmt.Errorf("remote access isn't available for %s/%s", goos, goarch)
}

func binName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensureBinary returns a verified cloudflared, downloading it the first time.
// HASHWATCH_CLOUDFLARED may point at an existing cloudflared instead.
func (m *Manager) ensureBinary(ctx context.Context) (string, error) {
	if p := os.Getenv("HASHWATCH_CLOUDFLARED"); p != "" {
		return p, nil
	}
	bin := filepath.Join(m.Dir, binName())
	sumFile := bin + ".sha256"
	if want, err := os.ReadFile(sumFile); err == nil {
		if got, err := fileSHA(bin); err == nil && got == strings.TrimSpace(string(want)) {
			return bin, nil
		}
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return "", err
	}
	m.set(func(s *Status) { s.State, s.Detail, s.Progress = "downloading", "Finding the latest cloudflared…", 0 })
	sum, err := m.download(ctx, bin)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(sumFile, []byte(sum+"\n"), 0o600); err != nil {
		return "", err
	}
	m.set(func(s *Status) { s.Progress, s.Detail = 1, "Verified" })
	return bin, nil
}

type release struct {
	Tag    string `json:"tag_name"`
	Body   string `json:"body"`
	Assets []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// ExpectedSHA returns the SHA-256 for asset, from the checksum list Cloudflare
// publishes in the release notes and GitHub's own asset digest. When both are
// present they must agree; at least one is required.
func ExpectedSHA(body, digest, asset string) (string, error) {
	var notes string
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(asset) + `:\s*([0-9a-f]{64})\s*$`)
	if mm := re.FindStringSubmatch(body); mm != nil {
		notes = mm[1]
	}
	gh := strings.TrimPrefix(strings.ToLower(digest), "sha256:")
	if len(gh) != 64 {
		gh = ""
	}
	switch {
	case notes != "" && gh != "" && notes != gh:
		return "", errors.New("published checksums disagree; not installing cloudflared")
	case notes != "":
		return notes, nil
	case gh != "":
		return gh, nil
	}
	return "", errors.New("no published checksum for " + asset)
}

func (m *Manager) download(ctx context.Context, bin string) (string, error) {
	asset, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	cl := &http.Client{Timeout: 10 * time.Minute}
	req, _ := http.NewRequestWithContext(ctx, "GET", ReleaseAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Hashwatch")
	r, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn't reach GitHub to download cloudflared: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("GitHub returned %s for the cloudflared release", r.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&rel); err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name != asset {
			continue
		}
		want, err := ExpectedSHA(rel.Body, a.Digest, asset)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(a.URL, "https://github.com/cloudflare/cloudflared/releases/download/") {
			return "", errors.New("unexpected download location for cloudflared")
		}
		m.set(func(s *Status) { s.Detail = "Downloading cloudflared " + rel.Tag + "…" })
		return m.fetch(ctx, cl, a.URL, a.Size, want, asset, bin)
	}
	return "", errors.New("this cloudflared release has no " + asset)
}

type progress struct {
	r     io.Reader
	n, of int64
	f     func(float64)
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.of > 0 {
		p.f(float64(p.n) / float64(p.of))
	}
	return n, err
}

func (m *Manager) fetch(ctx context.Context, cl *http.Client, url string, size int64, want, asset, bin string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Hashwatch")
	r, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("download failed: %s", r.Status)
	}
	if size <= 0 {
		size = r.ContentLength
	}
	tmp := bin + ".download"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pr := &progress{r: io.LimitReader(r.Body, 300<<20), of: size, f: func(x float64) {
		m.set(func(s *Status) { s.Progress = x * 0.98 })
	}}
	_, err = io.Copy(io.MultiWriter(f, h), pr)
	f.Close()
	defer os.Remove(tmp)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("cloudflared checksum mismatch (got %s…, want %s…); not installed", got[:12], want[:12])
	}
	if strings.HasSuffix(asset, ".tgz") {
		if err := untarOne(tmp, "cloudflared", bin+".new"); err != nil {
			return "", err
		}
	} else if err := copyFile(tmp, bin+".new"); err != nil {
		return "", err
	}
	if err := os.Chmod(bin+".new", 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(bin+".new", bin); err != nil {
		return "", err
	}
	return fileSHA(bin)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func untarOne(src, name, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return errors.New("cloudflared not found in the download")
		}
		if filepath.Base(h.Name) == name && h.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, 300<<20)); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	}
}
