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
	// Location groups miners by site (V2: several homes / sites in one dashboard). Empty = "Home".
	Location string `json:"location,omitempty"`
	// Opts holds this miner's automations and light effects (all opt-in).
	Opts MinerOpts `json:"opts,omitempty"`
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

// ConfigData is everything saved in config.json. New settings groups are
// separate structs so V2 (locations, push, a Pro tier) can add fields without
// touching existing ones; unknown fields from newer versions are ignored.
type ConfigData struct {
	Version      int             `json:"config_version,omitempty"`
	Miners       []Miner         `json:"miners"`
	PasswordHash string          `json:"password_hash,omitempty"`
	PasswordSalt string          `json:"password_salt,omitempty"`
	Remote       RemoteSettings  `json:"remote"`
	AlertsOff    map[string]bool `json:"alerts_off,omitempty"`
	NotifyOff    bool            `json:"desktop_notifications_off,omitempty"`
	MoveDeclined bool            `json:"move_to_applications_declined,omitempty"`
	// Port is the local port this copy last served on (kept so phone bookmarks keep working).
	Port     int              `json:"port,omitempty"`
	Security SecuritySettings `json:"security,omitempty"`
	Privacy  PrivacySettings  `json:"privacy,omitempty"`
	Fun      FunSettings      `json:"fun,omitempty"`
	// AllowWifi: phones on the same Wi-Fi can open HashWatch (bind 0.0.0.0).
	// nil in a brand-new file means off (listen on 127.0.0.1). An existing
	// settings file that never had this field keeps it on, matching the old default.
	AllowWifi *bool `json:"allow_wifi,omitempty"`
	// Gentle slows miner polling to 30 s so another dashboard can share the API.
	// Off by default. Failed-read backoff is unchanged (never faster than the interval).
	Gentle bool `json:"gentle,omitempty"`
	// Advanced enables pool URL/worker/password and reboot writes. Off by default.
	// The server rejects those writes unless this is on, even if the UI is bypassed.
	Advanced bool `json:"advanced_controls,omitempty"`
	// V2 placeholders (unused in v0.5.x, kept so the file format doesn't change later).
	Locations   []Location   `json:"locations,omitempty"`
	Plan        string       `json:"plan,omitempty"` // "" / "free"; V2 may add "pro"
	PushTargets []PushTarget `json:"push_targets,omitempty"`
}

type Config struct {
	ConfigData
	path string
	mu   sync.Mutex
}

// ConfigVersion is bumped when the file format changes meaningfully.
const ConfigVersion = 2

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
	existed, hadWifi := false, false
	if b, err := os.ReadFile(c.path); err == nil {
		existed = true
		_ = json.Unmarshal(b, &c.ConfigData)
		var raw map[string]json.RawMessage
		if json.Unmarshal(b, &raw) == nil {
			_, hadWifi = raw["allow_wifi"]
		}
	}
	if !hadWifi {
		// Upgrade: keep the previous "reachable on Wi-Fi" default. New install: off.
		v := existed
		c.AllowWifi = &v
	}
	c.Version = ConfigVersion
	return c, nil
}

// WifiOn reports whether phones on the same Wi-Fi may reach HashWatch.
func (c *Config) WifiOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.AllowWifi != nil && *c.AllowWifi
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.ConfigData
	out.Miners = nil
	for _, m := range c.Miners {
		if m.Kind != "remote" {
			out.Miners = append(out.Miners, m)
		}
	}
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

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o600) }
