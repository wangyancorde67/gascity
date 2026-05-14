// Package gas provides utilities for working with Ethereum gas prices.
//
// # Gas Price
//
// A [GasPrice] holds slow, standard, and fast fee estimates expressed in Gwei.
// Use [NewGasPrice] to construct a validated instance, and [GasPrice.ForLevel]
// to retrieve the Wei value for a specific [Level].
//
// # Price History
//
// [PriceHistory] maintains a bounded, chronologically-ordered ring of
// [PriceRecord] snapshots. Each record pairs a [GasPrice] with the UTC
// timestamp at which it was observed.
//
// Example usage:
//
//	h, err := gas.NewPriceHistory(100)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	price, _ := gas.NewGasPrice(20, 30, 50)
//	h.Add(price)
//
//	latest, _ := h.Latest()
//	fmt.Println(latest.Timestamp, latest.Price)
package gas
