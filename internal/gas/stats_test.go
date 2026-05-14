package gas

import (
	"math/big"
	"testing"
)

func TestComputeStats_InsufficientData(t *testing.T) {
	h, err := NewPriceHistory(10)
	if err != nil {
		t.Fatalf("unexpected error creating history: %v", err)
	}

	_, err = ComputeStats(h, LevelSlow)
	if err != ErrInsufficientData {
		t.Fatalf("expected ErrInsufficientData, got %v", err)
	}
}

func TestComputeStats_SingleEntry(t *testing.T) {
	h, _ := NewPriceHistory(10)

	p := makePrice(t, 10, 20, 30)
	h.Add(p)

	stats, err := ComputeStats(h, LevelFast)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := gweiToWei(big.NewInt(30))
	if stats.Average.Cmp(expected) != 0 {
		t.Errorf("average: got %s, want %s", stats.Average, expected)
	}
	if stats.Min.Cmp(expected) != 0 {
		t.Errorf("min: got %s, want %s", stats.Min, expected)
	}
	if stats.Max.Cmp(expected) != 0 {
		t.Errorf("max: got %s, want %s", stats.Max, expected)
	}
}

func TestComputeStats_MultipleEntries(t *testing.T) {
	h, _ := NewPriceHistory(10)

	h.Add(makePrice(t, 10, 20, 30))
	h.Add(makePrice(t, 20, 40, 60))
	h.Add(makePrice(t, 15, 30, 45))

	stats, err := ComputeStats(h, LevelSlow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// slow values: 10, 20, 15 gwei → avg = 15, min = 10, max = 20
	wantAvg := gweiToWei(big.NewInt(15))
	wantMin := gweiToWei(big.NewInt(10))
	wantMax := gweiToWei(big.NewInt(20))

	if stats.Average.Cmp(wantAvg) != 0 {
		t.Errorf("average: got %s, want %s", stats.Average, wantAvg)
	}
	if stats.Min.Cmp(wantMin) != 0 {
		t.Errorf("min: got %s, want %s", stats.Min, wantMin)
	}
	if stats.Max.Cmp(wantMax) != 0 {
		t.Errorf("max: got %s, want %s", stats.Max, wantMax)
	}
}

func TestComputeStats_InvalidLevel(t *testing.T) {
	h, _ := NewPriceHistory(10)
	h.Add(makePrice(t, 10, 20, 30))

	_, err := ComputeStats(h, Level(99))
	if err == nil {
		t.Fatal("expected error for invalid level, got nil")
	}
}
