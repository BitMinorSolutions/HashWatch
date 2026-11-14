package auto

import "testing"

func TestHotPointFollowsMiner(t *testing.T) {
	for _, c := range []struct {
		target, hot float64
		miner       bool
	}{{90, 90, true}, {80, 80, true}, {95, 95, true}, {0, FallbackHotC, false}, {250, FallbackHotC, false}} {
		if h, m := HotPoint(c.target); h != c.hot || m != c.miner {
			t.Errorf("target %v: %v %v", c.target, h, m)
		}
	}
}
