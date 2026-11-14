package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func launcher(s Spec) string { return filepath.Join(s.Dir, "start-hidden.vbs") }

func reg(args ...string) (string, error) {
	c := exec.Command("reg", args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := c.CombinedOutput()
	return string(out), err
}

func Get(s Spec) Status {
	st := Status{Supported: true, Method: "Startup (Run key)"}
	if out, err := reg("query", runKey, "/v", "Hashwatch"); err == nil && strings.Contains(out, "Hashwatch") {
		st.Enabled, st.Path = true, runKey+`\Hashwatch`
		st.Note = "Starts in the background when you sign in to Windows."
	}
	return st
}

func vbsQuote(a string) string { return `""` + strings.ReplaceAll(a, `"`, `""""`) + `""` }

func Enable(s Spec) (Status, error) {
	parts := []string{vbsQuote(s.Exe)}
	for _, a := range s.Args {
		parts = append(parts, vbsQuote(a))
	}
	// wscript runs the console app with a hidden window (0) and doesn't wait.
	vbs := fmt.Sprintf("CreateObject(\"WScript.Shell\").Run \"%s\", 0, False\r\n", strings.Join(parts, " "))
	if err := os.WriteFile(launcher(s), []byte(vbs), 0o644); err != nil {
		return Get(s), err
	}
	val := fmt.Sprintf(`wscript.exe "%s"`, launcher(s))
	if out, err := reg("add", runKey, "/v", "Hashwatch", "/t", "REG_SZ", "/d", val, "/f"); err != nil {
		return Get(s), fmt.Errorf("couldn't add the startup entry: %s", strings.TrimSpace(out))
	}
	return Get(s), nil
}

func Disable(s Spec) (Status, error) {
	reg("delete", runKey, "/v", "Hashwatch", "/f")
	os.Remove(launcher(s))
	return Get(s), nil
}
