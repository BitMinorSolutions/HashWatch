package app

import (
	"bytes"
	"image/png"
	"math"
	"testing"
)

func TestSparkline(t *testing.T) {
	if Sparkline([]float64{1, 2}, 64, 32) != nil {
		t.Fatal("too few points")
	}
	vals := []float64{3.9, 3.95, math.NaN(), 3.7, 3.8, 3.92, 3.94}
	b := Sparkline(vals, 64, 32)
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 32 {
		t.Fatal(err)
	}
	ink := 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				ink++
			}
		}
	}
	if ink < 60 || ink > 64*32/2 {
		t.Fatalf("ink %d", ink)
	}
}
