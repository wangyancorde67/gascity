package gas

// TrendDirection represents the direction of a gas price trend.
type TrendDirection int

const (
	// TrendStable indicates no significant change in gas prices.
	TrendStable TrendDirection = iota
	// TrendRising indicates gas prices are increasing.
	TrendRising
	// TrendFalling indicates gas prices are decreasing.
	TrendFalling
)

// String returns a human-readable representation of the trend direction.
func (t TrendDirection) String() string {
	switch t {
	case TrendRising:
		return "rising"
	case TrendFalling:
		return "falling"
	default:
		return "stable"
	}
}

// TrendResult holds the computed trend information for a price level.
type TrendResult struct {
	Direction  TrendDirection
	ChangeGwei float64
	ChangePct  float64
}

// thresholdPct is the minimum percentage change to be considered non-stable.
const thresholdPct = 2.0

// ComputeTrend calculates the trend between two consecutive GasPrice snapshots
// for the given level. Returns an error if the level is invalid or prices are nil.
func ComputeTrend(older, newer *GasPrice, level Level) (TrendResult, error) {
	if older == nil || newer == nil {
		return TrendResult{}, ErrInsufficientData
	}

	oldWei, err := older.ForLevel(level)
	if err != nil {
		return TrendResult{}, err
	}

	newWei, err := newer.ForLevel(level)
	if err != nil {
		return TrendResult{}, err
	}

	oldGwei := weiToGwei(oldWei)
	newGwei := weiToGwei(newWei)

	change := newGwei - oldGwei
	var pct float64
	if oldGwei != 0 {
		pct = (change / oldGwei) * 100.0
	}

	dir := TrendStable
	switch {
	case pct > thresholdPct:
		dir = TrendRising
	case pct < -thresholdPct:
		dir = TrendFalling
	}

	return TrendResult{
		Direction:  dir,
		ChangeGwei: change,
		ChangePct:  pct,
	}, nil
}
