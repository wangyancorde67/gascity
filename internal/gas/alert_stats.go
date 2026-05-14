package gas

import (
	"errors"
	"fmt"
)

// StatsAlert evaluates an alert level based on computed gas statistics
// for a given price level (e.g. "fast", "standard", "slow").
// It uses the average price for the specified level to determine the alert.
func StatsAlert(stats Stats, level string, thresholds AlertThresholds) (AlertLevel, error) {
	if stats.SampleCount == 0 {
		return AlertNone, errors.New("stats contain no samples")
	}

	priceWei, err := stats.Average.ForLevel(level)
	if err != nil {
		return AlertNone, fmt.Errorf("stats alert: %w", err)
	}

	// Convert from wei back to gwei for threshold comparison.
	priceGwei := weiToGwei(priceWei)
	return EvaluateAlert(priceGwei, thresholds)
}

// weiToGwei converts a wei value to gwei (integer division).
func weiToGwei(wei uint64) uint64 {
	const weiPerGwei = 1_000_000_000
	return wei / weiPerGwei
}

// AlertSummary holds alert evaluation results for all price levels.
type AlertSummary struct {
	Slow     AlertLevel
	Standard AlertLevel
	Fast     AlertLevel
}

// EvaluateAllLevels returns an AlertSummary by evaluating the alert level
// for each of the standard gas price levels using the provided stats and thresholds.
func EvaluateAllLevels(stats Stats, thresholds AlertThresholds) (AlertSummary, error) {
	var summary AlertSummary
	var err error

	summary.Slow, err = StatsAlert(stats, "slow", thresholds)
	if err != nil {
		return AlertSummary{}, fmt.Errorf("evaluate all levels (slow): %w", err)
	}

	summary.Standard, err = StatsAlert(stats, "standard", thresholds)
	if err != nil {
		return AlertSummary{}, fmt.Errorf("evaluate all levels (standard): %w", err)
	}

	summary.Fast, err = StatsAlert(stats, "fast", thresholds)
	if err != nil {
		return AlertSummary{}, fmt.Errorf("evaluate all levels (fast): %w", err)
	}

	return summary, nil
}
