package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxManifest = 1 << 20   // 1 MiB
	maxBinary   = 120 << 20 // 120 MiB
	defaultWait = 30 * time.Second
	binaryWait  = 2 * time.Minute
)

func defaultClient() *http.Client {
	return &http.Client{
		Timeout: binaryWait,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if !validUpdateURL(req.URL.String()) {
				return fmt.Errorf("redirect must stay on http(s)")
			}
			return nil
		},
	}
}

// FetchEnvelope downloads a signed manifest envelope from url.
func FetchEnvelope(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if !validUpdateURL(url) {
		return nil, fmt.Errorf("update URL must be http(s)")
	}
	if client == nil {
		client = defaultClient()
	}
	ctx, cancel := context.WithTimeout(ctx, defaultWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "HashWatch-Updater")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update manifest: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("update manifest: HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxManifest+1))
	if err != nil {
		return nil, fmt.Errorf("update manifest: %w", err)
	}
	if len(b) > maxManifest {
		return nil, fmt.Errorf("update manifest is too large")
	}
	return b, nil
}

// FetchBinary downloads a build and checks its SHA-256 (and size, if set).
func FetchBinary(ctx context.Context, client *http.Client, b Build) ([]byte, error) {
	if err := checkBuild(b); err != nil {
		return nil, err
	}
	if client == nil {
		client = defaultClient()
	}
	ctx, cancel := context.WithTimeout(ctx, binaryWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "HashWatch-Updater")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update download: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("update download: HTTP %d", res.StatusCode)
	}
	limit := int64(maxBinary)
	if b.Size > 0 && b.Size <= int64(maxBinary) {
		limit = b.Size
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("update download: %w", err)
	}
	if int64(len(body)) > int64(maxBinary) || (b.Size > 0 && int64(len(body)) != b.Size) {
		return nil, fmt.Errorf("update download: unexpected size")
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	want := strings.ToLower(strings.TrimSpace(b.SHA256))
	if got != want {
		return nil, fmt.Errorf("update download: SHA-256 does not match (got %s)", got)
	}
	return body, nil
}
