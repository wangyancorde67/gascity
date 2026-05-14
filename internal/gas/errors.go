package gas

import "errors"

// Sentinel errors for the gas package.
var (
	// ErrInsufficientData is returned when there is not enough data to
	// perform a computation (e.g. trend or stats calculation).
	ErrInsufficientData = errors.New("gas: insufficient data")

	// ErrInvalidLevel is returned when an unrecognised Level value is used.
	ErrInvalidLevel = errors.New("gas: invalid price level")

	// ErrInvalidSize is returned when a history buffer size is not positive.
	ErrInvalidSize = errors.New("gas: history size must be greater than zero")

	// ErrInvalidOrder is returned when gas price values are not in the
	// expected ascending order (slow ≤ average ≤ fast).
	ErrInvalidOrder = errors.New("gas: prices must satisfy slow <= average <= fast")

	// ErrZeroValue is returned when a gas price value is zero or negative.
	ErrZeroValue = errors.New("gas: price values must be greater than zero")
)
