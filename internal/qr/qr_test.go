package qr

import (
	"os"
	"strings"
	"testing"
)

func TestEncodeSizes(t *testing.T) {
	for _, n := range []int{1, 14, 15, 40, 62, 100, 200, 300} {
		c, err := Encode([]byte(strings.Repeat("a", n)))
		if err != nil || c.Size != c.Version*4+17 {
			t.Fatalf("len %d: %v", n, err)
		}
	}
	if dir := os.Getenv("QR_DUMP"); dir != "" {
		for i, s := range []string{"https://example.com", "https://quiet-river-sample-link.trycloudflare.com", "https://lucky-hash-miner-fresh-dawn.trycloudflare.com/#/overview", strings.Repeat("https://hashwatch.example.org/", 5), strings.Repeat("Q", 280), strings.Repeat("hello-wor", 30), strings.Repeat("ab", 125), strings.Repeat("k", 150), strings.Repeat("z", 190), strings.Repeat("y", 210), strings.Repeat("m", 120), strings.Repeat("r", 400)} {
			c, _ := Encode([]byte(s))
			os.WriteFile(dir+"/"+string(rune('a'+i))+".txt", []byte(s), 0o644)
			var b strings.Builder
			for _, row := range c.Modules {
				for _, m := range row {
					if m {
						b.WriteByte('1')
					} else {
						b.WriteByte('0')
					}
				}
				b.WriteByte('\n')
			}
			os.WriteFile(dir+"/"+string(rune('a'+i))+".mod", []byte(b.String()), 0o644)
		}
	}
}
