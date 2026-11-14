package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func unitPath(s Spec) string {
	return filepath.Join(home(s), ".config", "systemd", "user", "hashwatch.service")
}
func desktopPath(s Spec) string {
	return filepath.Join(home(s), ".config", "autostart", "hashwatch.desktop")
}

func quote(a string) string {
	if strings.ContainsAny(a, " \t\"\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
	}
	return a
}

func cmdline(s Spec) string {
	parts := []string{quote(s.Exe)}
	for _, a := range s.Args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Get(s Spec) Status {
	st := Status{Supported: true, Method: "systemd user service"}
	if _, err := os.Stat(unitPath(s)); err == nil {
		st.Enabled, st.Path = true, unitPath(s)
		st.Note = "Starts when you log in. On a headless Raspberry Pi, run: loginctl enable-linger $USER"
	} else if _, err := os.Stat(desktopPath(s)); err == nil {
		st.Enabled, st.Path, st.Method = true, desktopPath(s), "desktop autostart"
	}
	return st
}

func Enable(s Spec) (Status, error) {
	unit := fmt.Sprintf(`[Unit]
Description=%s miner dashboard
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, s.AppName, cmdline(s))
	p := unitPath(s)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Get(s), err
	}
	if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
		return Get(s), err
	}
	if s.Home == "" { // real install (tests skip systemctl)
		err := systemctl("daemon-reload")
		if err == nil {
			err = systemctl("enable", "hashwatch.service")
		}
		if err != nil { // no user systemd (some desktops / containers): XDG autostart instead
			os.Remove(p)
			return enableDesktop(s)
		}
	}
	return Get(s), nil
}

func enableDesktop(s Spec) (Status, error) {
	d := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=%s\nX-GNOME-Autostart-enabled=true\nNoDisplay=true\n", s.AppName, cmdline(s))
	p := desktopPath(s)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Get(s), err
	}
	if err := os.WriteFile(p, []byte(d), 0o644); err != nil { // loaded at next login
		return Get(s), err
	}
	return Get(s), nil
}

func Disable(s Spec) (Status, error) {
	if _, err := os.Stat(unitPath(s)); err == nil {
		if s.Home == "" {
			_ = systemctl("disable", "hashwatch.service")
		}
		if err := os.Remove(unitPath(s)); err != nil {
			return Get(s), err
		}
		if s.Home == "" {
			_ = systemctl("daemon-reload")
		}
	}
	os.Remove(desktopPath(s))
	return Get(s), nil
}
