package gas

import (
	"errors"
	"math/big"
)

// ErrInvalidGasPrice is returned when a gas price is invalid.
var ErrInvalidGasPrice = errors.New("invalid gas price")

// PriceLevel represents a named tier of gas price.
type PriceLevel string

const (
	PriceLevelSlow    PriceLevel = "slow"
	PriceLevelAverage PriceLevel = "average"
	PriceLevelFast    PriceLevel = "fast"
)

// GasPrice holds estimated gas prices for different speed tiers.
type GasPrice struct {
	Slow    *big.Int
	Average *big.Int
	Fast    *big.Int
}

// NewGasPrice creates a GasPrice from gwei values.
func NewGasPrice(slowGwei, avgGwei, fastGwei float64) (*GasPrice, error) {
	if slowGwei <= 0 || avgGwei <= 0 || fastGwei <= 0 {
		return nil, ErrInvalidGasPrice
	}
	if slowGwei > avgGwei || avgGwei > fastGwei {
		return nil, ErrInvalidGasPrice
	}
	return &GasPrice{
		Slow:    gweiToWei(slowGwei),
		Average: gweiToWei(avgGwei),
		Fast:    gweiToWei(fastGwei),
	}, nil
}

// ForLevel returns the gas price for the given level.
func (g *GasPrice) ForLevel(level PriceLevel) (*big.Int, error) {
	switch level {
	case PriceLevelSlow:
		return new(big.Int).Set(g.Slow), nil
	case PriceLevelAverage:
		return new(big.Int).Set(g.Average), nil
	case PriceLevelFast:
		return new(big.Int).Set(g.Fast), nil
	default:
		return nil, ErrInvalidGasPrice
	}
}

// gweiToWei converts a gwei float64 value to wei as *big.Int.
func gweiToWei(gwei float64) *big.Int {
	const weiPerGwei = 1e9
	weiFloat := new(big.Float).SetFloat64(gwei * weiPerGwei)
	weiInt, _ := weiFloat.Int(nil)
	return weiInt
}
