//go:build !darwin

package main

func nativeSupported() bool { return false }

// runNative isn't available on this OS: the dashboard opens in the browser instead.
func runNative(o shellOpts) bool { return false }
