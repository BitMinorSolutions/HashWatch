//go:build !linux && !darwin && !windows

package autostart

import "errors"

func Get(s Spec) Status { return Status{} }
func Enable(s Spec) (Status, error) {
	return Get(s), errors.New("not supported on this system")
}
func Disable(s Spec) (Status, error) { return Get(s), nil }
