//go:build !windows && !linux

package tunnel

import (
	"os/exec"
	"syscall"
)

func hideWindow(c *exec.Cmd) {}

func stopProc(c *exec.Cmd) error { return c.Process.Signal(syscall.SIGTERM) }
