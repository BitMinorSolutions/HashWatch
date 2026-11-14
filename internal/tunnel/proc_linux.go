package tunnel

import (
	"os/exec"
	"syscall"
)

// cloudflared exits if Hashwatch dies.
func hideWindow(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM} }

// stopProc asks cloudflared to shut down cleanly; WaitDelay kills it if it hangs.
func stopProc(c *exec.Cmd) error { return c.Process.Signal(syscall.SIGTERM) }
