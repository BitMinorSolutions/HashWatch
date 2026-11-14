// Command hwsign generates an ed25519 key and signs a HashWatch update manifest.
//
//	hwsign keygen -out dir
//	hwsign sign -key dir/ed25519.priv -in manifest.json -out manifest.signed.json
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashwatch/hashwatch/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: hwsign keygen -out DIR\n       hwsign sign -key PRIV -in manifest.json -out manifest.signed.json")
	}
	switch os.Args[1] {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ExitOnError)
		out := fs.String("out", ".", "directory to write ed25519.priv and ed25519.pub")
		_ = fs.Parse(os.Args[2:])
		if err := os.MkdirAll(*out, 0o700); err != nil {
			fatal(err)
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fatal(err)
		}
		privPath := filepath.Join(*out, "ed25519.priv")
		pubPath := filepath.Join(*out, "ed25519.pub")
		if err := os.WriteFile(privPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %s (keep this private)\nwrote %s\nbake the public key in with:\n  -ldflags \"-X github.com/hashwatch/hashwatch/internal/update.PublicKey=%s\"\n", privPath, pubPath, hex.EncodeToString(pub))
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		key := fs.String("key", "", "hex-encoded ed25519 private key file")
		in := fs.String("in", "", "manifest.json (the exact bytes that will be signed)")
		out := fs.String("out", "manifest.signed.json", "signed envelope")
		_ = fs.Parse(os.Args[2:])
		if *key == "" || *in == "" {
			fatal("sign: -key and -in are required")
		}
		kb, err := os.ReadFile(*key)
		if err != nil {
			fatal(err)
		}
		priv, err := hex.DecodeString(trim(kb))
		if err != nil || len(priv) != ed25519.PrivateKeySize {
			fatal("private key must be a 64-byte ed25519 key in hex")
		}
		payload, err := os.ReadFile(*in)
		if err != nil {
			fatal(err)
		}
		env, err := update.SignEnvelope(payload, ed25519.PrivateKey(priv))
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(*out, append(env, '\n'), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote %s\n", *out)
	default:
		fatal("unknown command " + os.Args[1])
	}
}

func trim(b []byte) string {
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

func fatal(v any) {
	fmt.Fprintln(os.Stderr, v)
	os.Exit(1)
}
