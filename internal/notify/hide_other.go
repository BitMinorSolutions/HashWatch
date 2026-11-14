//go:build !windows

package notify

import "os/exec"

func hide(c *exec.Cmd) {}
