package gas_test

import (
	"math/big"
	"testing"

	"github.com/gastownhall/gascity/internal/gas"
)

func TestNewGasPrice_Valid(t *testing.T) {
	gp, err := gas.NewGasPrice(10, 20, 30)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if gp == nil {
		t.Fatal("expected non-nil GasPrice")
	}

	expectedSlow := new(big.Int).SetInt64(10_000_000_000)
	if gp.Slow.Cmp(expectedSlow) != 0 {
		t.Errorf("Slow: expected %s, got %s", expectedSlow, gp.Slow)
	}
}

func TestNewGasPrice_InvalidOrder(t *testing.T) {
	_, err := gas.NewGasPrice(30, 20, 10)
	if err == nil {
		t.Fatal("expected error for inverted price order")
	}
}

func TestNewGasPrice_ZeroValue(t *testing.T) {
	_, err := gas.NewGasPrice(0, 20, 30)
	if err == nil {
		t.Fatal("expected error for zero slow price")
	}
}

func TestGasPrice_ForLevel(t *testing.T) {
	gp, err := gas.NewGasPrice(5, 15, 25)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	tests := []struct {
		level    gas.PriceLevel
		expected float64
	}{
		{gas.PriceLevelSlow, 5},
		{gas.PriceLevelAverage, 15},
		{gas.PriceLevelFast, 25},
	}

	for _, tt := range tests {
		price, err := gp.ForLevel(tt.level)
		if err != nil {
			t.Errorf("ForLevel(%s) error: %v", tt.level, err)
			continue
		}
		expected := new(big.Int).SetInt64(int64(tt.expected * 1e9))
		if price.Cmp(expected) != 0 {
			t.Errorf("ForLevel(%s): expected %s, got %s", tt.level, expected, price)
		}
	}
}

func TestGasPrice_ForLevel_Invalid(t *testing.T) {
	gp, _ := gas.NewGasPrice(5, 15, 25)
	_, err := gp.ForLevel("unknown")
	if err == nil {
		t.Fatal("expected error for unknown price level")
	}
}
