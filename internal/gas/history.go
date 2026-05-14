package gas

import (
	"errors"
	"sync"
	"time"
)

// ErrEmptyHistory is returned when no price history is available.
var ErrEmptyHistory = errors.New("gas: no price history available")

// PriceRecord represents a gas price snapshot at a point in time.
type PriceRecord struct {
	Timestamp time.Time
	Price     *GasPrice
}

// PriceHistory maintains a rolling window of historical gas price records.
type PriceHistory struct {
	mu      sync.RWMutex
	records []PriceRecord
	maxSize int
}

// NewPriceHistory creates a PriceHistory with the given maximum number of records.
// maxSize must be greater than zero.
func NewPriceHistory(maxSize int) (*PriceHistory, error) {
	if maxSize <= 0 {
		return nil, errors.New("gas: history maxSize must be greater than zero")
	}
	return &PriceHistory{
		records: make([]PriceRecord, 0, maxSize),
		maxSize: maxSize,
	}, nil
}

// Add appends a new PriceRecord to the history. If the history is full,
// the oldest record is evicted.
func (h *PriceHistory) Add(p *GasPrice) {
	h.mu.Lock()
	defer h.mu.Unlock()

	record := PriceRecord{
		Timestamp: time.Now().UTC(),
		Price:     p,
	}

	if len(h.records) >= h.maxSize {
		// Evict oldest by shifting left.
		h.records = append(h.records[1:], record)
		return
	}
	h.records = append(h.records, record)
}

// Latest returns the most recently added PriceRecord.
func (h *PriceHistory) Latest() (PriceRecord, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.records) == 0 {
		return PriceRecord{}, ErrEmptyHistory
	}
	return h.records[len(h.records)-1], nil
}

// All returns a copy of all stored records in chronological order.
func (h *PriceHistory) All() []PriceRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	copy := make([]PriceRecord, len(h.records))
	for i, r := range h.records {
		copy[i] = r
	}
	return copy
}

// Len returns the current number of records stored.
func (h *PriceHistory) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.records)
}
