package autostart

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func plistPath(s Spec) string {
	return filepath.Join(home(s), "Library", "LaunchAgents", Label+".plist")
}

func Get(s Spec) Status {
	st := Status{Supported: true, Method: "LaunchAgent"}
	if _, err := os.Stat(plistPath(s)); err == nil {
		st.Enabled, st.Path = true, plistPath(s)
		st.Note = "Starts when you log in. Don't move the app after turning this on."
	}
	return st
}

func Enable(s Spec) (Status, error) {
	var args strings.Builder
	for _, a := range append([]string{s.Exe}, s.Args...) {
		args.WriteString("\t\t<string>" + html.EscapeString(a) + "</string>\n")
	}
	logf := html.EscapeString(filepath.Join(s.Dir, "hashwatch.log"))
	p := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, Label, args.String(), logf, logf)
	path := plistPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Get(s), err
	}
	if err := os.WriteFile(path, []byte(p), 0o644); err != nil { // loaded at next login
		return Get(s), err
	}
	return Get(s), nil
}

func Disable(s Spec) (Status, error) {
	if s.Home == "" {
		_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)).Run()
	}
	if err := os.Remove(plistPath(s)); err != nil && !os.IsNotExist(err) {
		return Get(s), err
	}
	return Get(s), nil
}
