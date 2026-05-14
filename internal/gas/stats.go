package gas

import (
	"errors"
	"math/big"
)

// ErrInsufficientData is returned when there are not enough data points
// to compute a meaningful statistic.
var ErrInsufficientData = errors.New("gas: insufficient data points")

// Stats holds computed statistics over a set of gas prices.
type Stats struct {
	// Average is the mean gas price across all levels and samples.
	Average *big.Int
	// Min is the lowest gas price observed across all samples.
	Min *big.Int
	// Max is the highest gas price observed across all samples.
	Max *big.Int
}

// ComputeStats calculates basic statistics (average, min, max) from the
// prices stored in the given PriceHistory for the specified Level.
// It returns ErrInsufficientData when the history contains no entries.
func ComputeStats(h *PriceHistory, level Level) (*Stats, error) {
	prices := h.Snapshot()
	if len(prices) == 0 {
		return nil, ErrInsufficientData
	}

	sum := new(big.Int)
	var min, max *big.Int

	for _, p := range prices {
		v, err := p.ForLevel(level)
		if err != nil {
			return nil, err
		}

		sum.Add(sum, v)

		if min == nil || v.Cmp(min) < 0 {
			min = new(big.Int).Set(v)
		}
		if max == nil || v.Cmp(max) > 0 {
			max = new(big.Int).Set(v)
		}
	}

	count := big.NewInt(int64(len(prices)))
	avg := new(big.Int).Div(sum, count)

	return &Stats{
		Average: avg,
		Min:     min,
		Max:     max,
	}, nil
}
