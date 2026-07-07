package biz

import (
	"math"
	"testing"
)

func TestCalcCreditsBasic(t *testing.T) {
	price := &modelPriceEntry{inputPrice: 2.0, outputPrice: 8.0} // per-million CNY
	credits, micro, cost := calcCredits(price, 1_000_000, 500_000, 0, 0, 0.01)

	wantCost := 2.0 + 4.0 // 1M input * 2/M + 0.5M output * 8/M
	if math.Abs(cost-wantCost) > 1e-9 {
		t.Fatalf("costCNY = %v, want %v", cost, wantCost)
	}
	wantCredits := wantCost / 0.01
	if math.Abs(credits-wantCredits) > 1e-9 {
		t.Fatalf("credits = %v, want %v", credits, wantCredits)
	}
	if micro != int64(math.Round(wantCredits*1_000_000)) {
		t.Fatalf("microCredits = %d", micro)
	}
}

func TestCalcCreditsCacheReadFallsBackToInputPrice(t *testing.T) {
	price := &modelPriceEntry{inputPrice: 10.0, outputPrice: 0}
	_, _, cost := calcCredits(price, 0, 0, 2_000_000, 0, 0)
	if math.Abs(cost-20.0) > 1e-9 {
		t.Fatalf("cache-read tokens should be priced at input price when no cache price set, cost=%v", cost)
	}
}

func TestCalcCreditsUsesDedicatedCachePrice(t *testing.T) {
	price := &modelPriceEntry{inputPrice: 10.0, cacheReadPrice: 1.0}
	_, _, cost := calcCredits(price, 0, 0, 1_000_000, 0, 0)
	if math.Abs(cost-1.0) > 1e-9 {
		t.Fatalf("dedicated cache price ignored, cost=%v", cost)
	}
}

func TestCalcCreditsNoPricing(t *testing.T) {
	credits, micro, cost := calcCredits(&modelPriceEntry{noPricing: true}, 100, 100, 0, 0, 0.01)
	if credits != 0 || micro != 0 || cost != 0 {
		t.Fatal("noPricing entry must produce zero cost")
	}
}

func TestCalcCreditsCacheWriteUsesDedicatedPrice(t *testing.T) {
	price := &modelPriceEntry{inputPrice: 10.0, cacheWritePrice: 3.0}
	_, _, cost := calcCredits(price, 0, 0, 0, 1_000_000, 0)
	if math.Abs(cost-3.0) > 1e-9 {
		t.Fatalf("dedicated cache-write price ignored, cost=%v", cost)
	}
}

func TestCalcCreditsCacheWriteFallsBackToInputPrice(t *testing.T) {
	price := &modelPriceEntry{inputPrice: 10.0, outputPrice: 0}
	_, _, cost := calcCredits(price, 0, 0, 0, 2_000_000, 0)
	if math.Abs(cost-20.0) > 1e-9 {
		t.Fatalf("cache-write tokens should be priced at input price when no cache-write price set, cost=%v", cost)
	}
}

func TestCalcCreditsZeroRateMeansNoCredits(t *testing.T) {
	price := &modelPriceEntry{inputPrice: 2.0, outputPrice: 8.0}
	credits, micro, cost := calcCredits(price, 1_000_000, 0, 0, 0, 0)
	if cost <= 0 {
		t.Fatal("cost should still be computed")
	}
	if credits != 0 || micro != 0 {
		t.Fatal("zero rate must not mint credits")
	}
}
