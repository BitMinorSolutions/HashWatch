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

func TestHeatThresholdHelpers(t *testing.T) {
	if AvgRunningHot(84, 85) || AvgRunningHot(87, 85) || AvgRunningHot(90, 90) || AvgRunningHot(92, 90) {
		t.Fatal("normal regulation should not count as running hot")
	}
	if !AvgRunningHot(88, 85) || !AvgRunningHot(93, 90) {
		t.Fatal("average well above target should count as running hot")
	}
	if ChipUnsafe(92) || ChipUnsafe(94.9) {
		t.Fatal("hottest chip under 95°C should be safe")
	}
	if !ChipUnsafe(95) || !ChipUnsafe(100) {
		t.Fatal("hottest chip at or above 95°C should be unsafe")
	}
	if DHHigh(1) || DHHigh(2.542) || DHHigh(4.999) {
		t.Fatal("typical DH should not warn")
	}
	if !DHHigh(5) || !DHHigh(6.25) {
		t.Fatal("DH at or above 5% should warn")
	}
}
