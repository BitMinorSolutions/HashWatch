package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors (SHA1, secret "12345678901234567890"), last 6 digits.
func TestRFC6238(t *testing.T) {
	sec := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037", 20000000000: "353130"} {
		got, err := Code(sec, Counter(time.Unix(ts, 0)))
		if err != nil || got != want {
			t.Errorf("t=%d got %s want %s (%v)", ts, got, want, err)
		}
	}
}

func TestVerifyWindowAndReplay(t *testing.T) {
	sec := NewSecret()
	now := time.Unix(1794642000, 0)
	prev, _ := Code(sec, Counter(now)-1)
	c, ok := Verify(sec, prev, now, 0)
	if !ok || c != Counter(now)-1 {
		t.Fatal("previous step should be accepted (clock drift)")
	}
	if _, ok := Verify(sec, prev, now, c); ok {
		t.Fatal("reused code must be refused")
	}
	old, _ := Code(sec, Counter(now)-3)
	if _, ok := Verify(sec, old, now, 0); ok {
		t.Fatal("code from 90 s ago must be refused")
	}
	if _, ok := Verify(sec, "12345", now, 0); ok {
		t.Fatal("short code")
	}
}

func TestURIAndRecovery(t *testing.T) {
	u := URI("HashWatch", "Nano 3", "ABC")
	if !strings.HasPrefix(u, "otpauth://totp/HashWatch:Nano%203?") || !strings.Contains(u, "secret=ABC") {
		t.Fatal(u)
	}
	rc := RecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range rc {
		if len(c) != 9 || c[4] != '-' || seen[c] {
			t.Fatal(rc)
		}
		seen[c] = true
	}
}
