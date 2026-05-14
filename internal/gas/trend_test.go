package gas

import (
	"math/big"
	"testing"
)

func makePriceFromGwei(slow, avg, fast float64) *GasPrice {
	slowWei := new(big.Int).SetUint64(uint64(slow * 1e9))
	avgWei := new(big.Int).SetUint64(uint64(avg * 1e9))
	fastWei := new(big.Int).SetUint64(uint64(fast * 1e9))
	return &GasPrice{slow: slowWei, average: avgWei, fast: fastWei}
}

func TestComputeTrend_Rising(t *testing.T) {
	older := makePriceFromGwei(10, 20, 30)
	newer := makePriceFromGwei(15, 25, 35)

	result, err := ComputeTrend(older, newer, LevelAverage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Direction != TrendRising {
		t.Errorf("expected TrendRising, got %s", result.Direction)
	}
	if result.ChangePct <= 0 {
		t.Errorf("expected positive change pct, got %f", result.ChangePct)
	}
}

func TestComputeTrend_Falling(t *testing.T) {
	older := makePriceFromGwei(20, 40, 60)
	newer := makePriceFromGwei(10, 20, 30)

	result, err := ComputeTrend(older, newer, LevelFast)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Direction != TrendFalling {
		t.Errorf("expected TrendFalling, got %s", result.Direction)
	}
}

func TestComputeTrend_Stable(t *testing.T) {
	older := makePriceFromGwei(10, 20, 30)
	newer := makePriceFromGwei(10, 20.1, 30)

	result, err := ComputeTrend(older, newer, LevelAverage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Direction != TrendStable {
		t.Errorf("expected TrendStable, got %s", result.Direction)
	}
}

func TestComputeTrend_NilOlder(t *testing.T) {
	newer := makePriceFromGwei(10, 20, 30)
	_, err := ComputeTrend(nil, newer, LevelAverage)
	if err != ErrInsufficientData {
		t.Errorf("expected ErrInsufficientData, got %v", err)
	}
}

func TestComputeTrend_NilNewer(t *testing.T) {
	older := makePriceFromGwei(10, 20, 30)
	_, err := ComputeTrend(older, nil, LevelAverage)
	if err != ErrInsufficientData {
		t.Errorf("expected ErrInsufficientData, got %v", err)
	}
}

func TestTrendDirection_String(t *testing.T) {
	cases := []struct {
		dir      TrendDirection
		expected string
	}{
		{TrendStable, "stable"},
		{TrendRising, "rising"},
		{TrendFalling, "falling"},
	}
	for _, tc := range cases {
		if got := tc.dir.String(); got != tc.expected {
			t.Errorf("expected %q, got %q", tc.expected, got)
		}
	}
}
