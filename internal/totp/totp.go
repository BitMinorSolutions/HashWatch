// Package totp implements RFC 6238 time-based one-time passwords (the 6-digit
// codes from Google Authenticator, 1Password, Authy…): HMAC-SHA1, 30-second
// steps, 6 digits, and a ±1 step window for clock drift.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	Step   = 30
	Digits = 6
	Window = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret, base32 encoded.
func NewSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

// Code is the code for a counter value (RFC 4226 HOTP).
func Code(secret string, counter uint64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000), nil
}

// Counter is the time step for t.
func Counter(t time.Time) uint64 { return uint64(t.Unix() / Step) }

// Verify checks code at time t and returns the matching counter (so callers can
// refuse a code that was already used). ok is false for a wrong or reused code.
func Verify(secret, code string, t time.Time, lastUsed uint64) (counter uint64, ok bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != Digits {
		return 0, false
	}
	c := Counter(t)
	for d := -Window; d <= Window; d++ {
		n := uint64(int64(c) + int64(d))
		want, err := Code(secret, n)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			if n <= lastUsed {
				return n, false // replay
			}
			return n, true
		}
	}
	return 0, false
}

// URI is the otpauth:// link an authenticator app reads from a QR code.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// RecoveryCodes returns n random one-time codes like "7f3k-9q2m".
func RecoveryCodes(n int) []string {
	const alpha = "23456789abcdefghjkmnpqrstuvwxyz"
	out := make([]string, n)
	for i := range out {
		b := make([]byte, 8)
		rand.Read(b)
		var s strings.Builder
		for j, x := range b {
			if j == 4 {
				s.WriteByte('-')
			}
			s.WriteByte(alpha[int(x)%len(alpha)])
		}
		out[i] = s.String()
	}
	return out
}
