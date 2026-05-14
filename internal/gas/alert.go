package gas

import (
	"errors"
	"fmt"
)

// AlertLevel represents the severity of a gas price alert.
type AlertLevel int

const (
	// AlertNone indicates no alert condition.
	AlertNone AlertLevel = iota
	// AlertWarning indicates gas prices are elevated.
	AlertWarning
	// AlertCritical indicates gas prices are critically high.
	AlertCritical
)

// String returns a human-readable representation of the AlertLevel.
func (a AlertLevel) String() string {
	switch a {
	case AlertNone:
		return "none"
	case AlertWarning:
		return "warning"
	case AlertCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// AlertThresholds defines the gwei thresholds that trigger alert levels.
type AlertThresholds struct {
	// Warning is the gwei value above which a warning alert is raised.
	Warning uint64
	// Critical is the gwei value above which a critical alert is raised.
	Critical uint64
}

// Validate checks that the thresholds are consistent.
func (t AlertThresholds) Validate() error {
	if t.Warning == 0 {
		return errors.New("warning threshold must be greater than zero")
	}
	if t.Critical == 0 {
		return errors.New("critical threshold must be greater than zero")
	}
	if t.Warning >= t.Critical {
		return fmt.Errorf("warning threshold (%d) must be less than critical threshold (%d)", t.Warning, t.Critical)
	}
	return nil
}

// EvaluateAlert returns the AlertLevel for a given gas price in gwei
// based on the provided thresholds.
func EvaluateAlert(priceGwei uint64, thresholds AlertThresholds) (AlertLevel, error) {
	if err := thresholds.Validate(); err != nil {
		return AlertNone, fmt.Errorf("invalid thresholds: %w", err)
	}
	switch {
	case priceGwei >= thresholds.Critical:
		return AlertCritical, nil
	case priceGwei >= thresholds.Warning:
		return AlertWarning, nil
	default:
		return AlertNone, nil
	}
}
