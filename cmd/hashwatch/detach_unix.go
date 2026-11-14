//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the background server in its own session so it outlives the launcher.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
