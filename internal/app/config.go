package app

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// AppName is the product name used everywhere in the UI. Rename here.
// AppName is the product name shown everywhere (a private build may override it with -ldflags -X).
var AppName = "HashWatch"

// Tagline is shown under the logo, on the login/setup screens, in About and in the DMG window.
// Keep it the single source of truth (the spelling may change).
const Tagline = "Powered by GrumpTech"

// dirName is the settings folder name. It stays "Hashwatch" so existing settings keep working.
var dirName = "Hashwatch"

type Miner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	Kind string `json:"kind"` // "cgminer" or "remote"
	// remote-only (never saved to disk; set from flags)
	RemoteURL  string `json:"-"`
	RemoteUser string `json:"-"`
	RemotePass string `json:"-"`
}

// RemoteSettings is the built-in remote access (Cloudflare tunnel) setup.
type RemoteSettings struct {
	Enabled  bool   `json:"enabled"`
	Token    string `json:"token,omitempty"`    // optional named-tunnel token (permanent link)
	Hostname string `json:"hostname,omitempty"` // hostname that token routes to
	LastURL  string `json:"last_url,omitempty"`
}

type Config struct {
	Miners       []Miner         `json:"miners"`
	PasswordHash string          `json:"password_hash,omitempty"`
	PasswordSalt string          `json:"password_salt,omitempty"`
	Remote       RemoteSettings  `json:"remote"`
	AlertsOff    map[string]bool `json:"alerts_off,omitempty"`
	NotifyOff    bool            `json:"desktop_notifications_off,omitempty"`
	MoveDeclined bool            `json:"move_to_applications_declined,omitempty"`
	// Port is the local port this copy last served on (kept so phone bookmarks keep working).
	Port int `json:"port,omitempty"`

	path string
	mu   sync.Mutex
}

func DefaultDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, dirName)
}

func LoadConfig(dir string) (*Config, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Config{path: filepath.Join(dir, "config.json")}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, c)
	}
	return c, nil
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var saved []Miner
	for _, m := range c.Miners {
		if m.Kind != "remote" {
			saved = append(saved, m)
		}
	}
	out := struct {
		Miners       []Miner         `json:"miners"`
		PasswordHash string          `json:"password_hash,omitempty"`
		PasswordSalt string          `json:"password_salt,omitempty"`
		Remote       RemoteSettings  `json:"remote"`
		AlertsOff    map[string]bool `json:"alerts_off,omitempty"`
		NotifyOff    bool            `json:"desktop_notifications_off,omitempty"`
		MoveDeclined bool            `json:"move_to_applications_declined,omitempty"`
		Port         int             `json:"port,omitempty"`
	}{saved, c.PasswordHash, c.PasswordSalt, c.Remote, c.AlertsOff, c.NotifyOff, c.MoveDeclined, c.Port}
	b, _ := json.MarshalIndent(out, "", "  ")
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// AlertOn reports whether an alert kind is enabled (all are on by default).
func (c *Config) AlertOn(kind string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.AlertsOff[kind]
}

func hashPw(pw, saltHex string) string {
	salt, _ := hex.DecodeString(saltHex)
	k, _ := pbkdf2.Key(sha256.New, pw, salt, 210000, 32)
	return hex.EncodeToString(k)
}

func (c *Config) SetPassword(pw string) {
	if pw == "" {
		c.PasswordHash, c.PasswordSalt = "", ""
		return
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	c.PasswordSalt = hex.EncodeToString(salt)
	c.PasswordHash = hashPw(pw, c.PasswordSalt)
}

func (c *Config) CheckPassword(pw string) bool {
	if c.PasswordHash == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(hashPw(pw, c.PasswordSalt)), []byte(c.PasswordHash)) == 1
}

func NewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
