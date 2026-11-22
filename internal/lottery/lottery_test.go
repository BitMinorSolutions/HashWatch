package lottery

import (
	"math"
	"testing"
)

func near(a, b, rel float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= rel*math.Max(math.Abs(a), math.Abs(b))
}

func TestHashesPerBlock(t *testing.T) {
	// difficulty 1 = 2^32 hashes by definition
	if HashesPerBlock(1) != 4294967296 {
		t.Fatal(HashesPerBlock(1))
	}
	if got := HashesPerBlock(134823837323272.7); !near(got, 134823837323272.7*4294967296, 1e-12) {
		t.Fatal(got)
	}
}

func TestNano3Odds(t *testing.T) {
	// A ~4 TH/s Nano 3 at difficulty 134.82 T (Nov 2026).
	h, d := 4e12, 134.8238373232727e12
	o := Compute(h, d, 423759812)
	hpb := d * 4294967296 // ≈ 5.79e23
	wantYears := hpb / h / (365.25 * 86400)
	if !near(o.YearsPerBlock, wantYears, 1e-12) || o.YearsPerBlock < 4500 || o.YearsPerBlock > 4700 {
		t.Fatalf("years per block %v (want %v)", o.YearsPerBlock, wantYears)
	}
	// λ for one day; chance ≈ λ when λ is tiny
	lday := h * 86400 / hpb
	if !near(o.PerDay, lday, 1e-6) {
		t.Fatalf("per day %v vs λ %v", o.PerDay, lday)
	}
	if !near(o.OneInPerDay, 1/o.PerDay, 1e-12) || o.OneInPerDay < 1.6e6 || o.OneInPerDay > 1.8e6 {
		t.Fatalf("1 in %v per day", o.OneInPerDay)
	}
	// per block chance = share of network hashrate (to first order)
	if !near(o.PerBlock, o.Share, 1e-6) {
		t.Fatalf("per block %v vs share %v", o.PerBlock, o.Share)
	}
	// per year = 1 - e^-λyear, and expected blocks per year = 1/years-per-block
	ly := h * 365.25 * 86400 / hpb
	if !near(o.PerYear, 1-math.Exp(-ly), 1e-9) || !near(o.ExpectedPerYear, 1/o.YearsPerBlock, 1e-12) {
		t.Fatalf("per year %v expected %v", o.PerYear, o.ExpectedPerYear)
	}
	if !near(o.BestShareClose, 423759812/d, 1e-12) {
		t.Fatal(o.BestShareClose)
	}
}

func TestChanceSaturates(t *testing.T) {
	// with λ = 1 the chance of at least one block is 1 - 1/e, never "100%"
	d := 1.0
	h := HashesPerBlock(d) / 600
	p := 1 / HashesPerBlock(d)
	want := -math.Expm1((h * 600) * math.Log1p(-p))
	if c := Chance(h, d, 600); !near(c, want, 1e-12) || c >= 1 {
		t.Fatal(c)
	}
	// Still agrees with the Poisson 1-e^{-λ} to many digits (p is ~2^-32).
	if !near(Chance(h, d, 600), 1-1/math.E, 1e-9) {
		t.Fatal("should still be ~1-1/e")
	}
	if Chance(0, d, 600) != 0 || Chance(h, 0, 600) != 0 {
		t.Fatal("zero inputs")
	}
}

func TestSubsidyAndReward(t *testing.T) {
	cases := map[int64]int64{0: 50e8, 209999: 50e8, 210000: 25e8, 840000: 3.125e8, 975766: 3.125e8, 1050000: 1.5625e8, 64 * 210000: 0}
	for h, want := range cases {
		if got := Subsidy(h); got != want {
			t.Errorf("subsidy(%d)=%d want %d", h, got, want)
		}
	}
	r := BlockReward(975765, 1_000_000, 67017)
	if r.Height != 975766 || r.TotalSat != 312_500_000+1_000_000 || !near(r.USD, 3.135*67017, 1e-12) {
		t.Fatalf("%+v", r)
	}
	if BlockReward(975765, 0, 0).USD != 0 {
		t.Fatal("no price must mean no dollars")
	}
}

func TestCoinFlipsAgreesWithLotteryCard(t *testing.T) {
	// 5.1 TH/s at the Nov 2026 difficulty used in TestNano3Odds.
	h, d := 5.1e12, 134.8238373232727e12
	o := Compute(h, d, 76205629)
	if o.OneInPerDay < 1.2e6 || o.OneInPerDay > 1.4e6 {
		t.Fatalf("1 in %v per day", o.OneInPerDay)
	}
	// The lottery card uses the same OneInPerDay (Poisson, ≈ λ for these odds).
	if !near(o.OneInPerDay, 1/o.PerDay, 1e-12) {
		t.Fatal("card and phrase must share one_in_per_day")
	}
	if flips := CoinFlips(o.OneInPerDay); flips != 20 {
		t.Fatalf("1 in %v -> %d flips, want 20", o.OneInPerDay, flips)
	}
	if CoinFlips(0) != 0 || CoinFlips(1) != 0 {
		t.Fatal("tiny odds")
	}
}

func TestChanceIsNotALinearSum(t *testing.T) {
	// A hashrate expected to find 2 blocks a year must not report a 200% chance.
	d := 1.0
	h := 2 * HashesPerBlock(d) / Year
	c := Chance(h, d, Year)
	if c >= 1 || c <= 0.8 || !near(c, 1-math.Exp(-2), 1e-9) {
		t.Fatalf("chance %v, want 1-e^-2", c)
	}
	if !near(Lambda(h, d, Year), 2, 1e-12) {
		t.Fatal("expected count is still 2")
	}
	// Over one day, 1-(1-p)^n and λ still agree for a Nano 3.
	h, d = 5.1e12, 134.8238373232727e12
	p := Chance(h, d, Day)
	lam := Lambda(h, d, Day)
	if !near(p, lam, 1e-6) || p >= 1 {
		t.Fatalf("day chance %v λ %v", p, lam)
	}
}
