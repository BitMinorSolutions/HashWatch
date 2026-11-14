package source

import "testing"

// The remote/preview source must never be able to send control commands.
func TestRemoteIsNotAController(t *testing.T) {
	var s Source = &Remote{}
	if _, ok := s.(Controller); ok {
		t.Fatal("Remote must not implement Controller")
	}
	var c Source = CGMiner{}
	if _, ok := c.(Controller); !ok {
		t.Fatal("CGMiner should implement Controller")
	}
}
