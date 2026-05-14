package gas_test

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/gas"
)

func makePrice(t *testing.T, slow, standard, fast uint64) *gas.GasPrice {
	t.Helper()
	p, err := gas.NewGasPrice(slow, standard, fast)
	if err != nil {
		t.Fatalf("NewGasPrice: %v", err)
	}
	return p
}

func TestNewPriceHistory_InvalidSize(t *testing.T) {
	_, err := gas.NewPriceHistory(0)
	if err == nil {
		t.Fatal("expected error for maxSize=0, got nil")
	}
}

func TestPriceHistory_AddAndLen(t *testing.T) {
	h, err := gas.NewPriceHistory(5)
	if err != nil {
		t.Fatalf("NewPriceHistory: %v", err)
	}

	if h.Len() != 0 {
		t.Fatalf("expected Len=0, got %d", h.Len())
	}

	h.Add(makePrice(t, 10, 20, 30))
	h.Add(makePrice(t, 11, 21, 31))

	if h.Len() != 2 {
		t.Fatalf("expected Len=2, got %d", h.Len())
	}
}

func TestPriceHistory_Eviction(t *testing.T) {
	h, _ := gas.NewPriceHistory(3)

	for i := uint64(1); i <= 5; i++ {
		h.Add(makePrice(t, i*10, i*20, i*30))
	}

	if h.Len() != 3 {
		t.Fatalf("expected Len=3 after eviction, got %d", h.Len())
	}

	records := h.All()
	// The oldest three entries should be records 3, 4, 5 (i=3,4,5).
	if records[0].Price == nil {
		t.Fatal("expected non-nil price in oldest retained record")
	}
}

func TestPriceHistory_Latest_Empty(t *testing.T) {
	h, _ := gas.NewPriceHistory(10)
	_, err := h.Latest()
	if err != gas.ErrEmptyHistory {
		t.Fatalf("expected ErrEmptyHistory, got %v", err)
	}
}

func TestPriceHistory_Latest(t *testing.T) {
	h, _ := gas.NewPriceHistory(10)

	h.Add(makePrice(t, 5, 10, 15))
	time.Sleep(time.Millisecond) // ensure distinct timestamps
	h.Add(makePrice(t, 50, 100, 150))

	rec, err := h.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}

	level, err := rec.Price.ForLevel(gas.Fast)
	if err != nil {
		t.Fatalf("ForLevel: %v", err)
	}
	if level.Uint64() != 150_000_000_000 { // 150 gwei in wei
		t.Fatalf("unexpected fast price: %s", level)
	}
}

func TestPriceHistory_AllReturnsCopy(t *testing.T) {
	h, _ := gas.NewPriceHistory(5)
	h.Add(makePrice(t, 1, 2, 3))

	a := h.All()
	a[0].Price = nil // mutate the copy

	b := h.All()
	if b[0].Price == nil {
		t.Fatal("All() should return an independent copy")
	}
}
