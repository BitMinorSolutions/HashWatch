// Package notify shows a native desktop notification. Text is passed to the
// OS helper as arguments / environment variables, never spliced into a script.
package notify

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// AppName labels notifications (set by main).
var AppName = "HashWatch"

// Native, when set (by the macOS/Windows app shell), shows notifications natively instead.
var Native func(title, body string) error

// Command builds the OS-specific notification command (exported for tests).
func Command(title, body string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv) sound name \"Glass\"", "-e", "end run", title, body), nil
	case "windows":
		ps := `$ErrorActionPreference='Stop';` +
			`[Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]>$null;` +
			`$x=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02);` +
			`$t=$x.GetElementsByTagName('text');$t.Item(0).AppendChild($x.CreateTextNode($env:HW_TITLE))>$null;$t.Item(1).AppendChild($x.CreateTextNode($env:HW_BODY))>$null;` +
			`$n=[Windows.UI.Notifications.ToastNotification]::new($x);` +
			`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($n)`
		c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", ps)
		c.Env = append(os.Environ(), "HW_TITLE="+title, "HW_BODY="+body)
		hide(c)
		return c, nil
	default:
		if p, err := exec.LookPath("notify-send"); err == nil {
			return exec.Command(p, "-a", AppName, title, body), nil
		}
		if p, err := exec.LookPath("gdbus"); err == nil {
			return exec.Command(p, "call", "--session", "--dest=org.freedesktop.Notifications", "--object-path=/org/freedesktop/Notifications",
				"--method=org.freedesktop.Notifications.Notify", AppName, "0", "", title, body, "[]", "{}", "8000"), nil
		}
		return nil, errors.New("no desktop notification service found (install libnotify / notify-send)")
	}
}

// Send shows the notification and returns without waiting for it to close.
func Send(title, body string) error {
	if Native != nil {
		if err := Native(title, body); err == nil {
			return nil
		}
	}
	c, err := Command(title, body)
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
}
