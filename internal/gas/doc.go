// Package gas provides utilities for estimating and working with
// Ethereum gas prices across multiple speed tiers (slow, average, fast).
//
// # Overview
//
// The primary type is [GasPrice], which holds wei-denominated gas price
// estimates for three named levels:
//
//   - [PriceLevelSlow]    — lower cost, longer confirmation time
//   - [PriceLevelAverage] — balanced cost and confirmation time
//   - [PriceLevelFast]    — higher cost, faster confirmation
//
// # Usage
//
//	gp, err := gas.NewGasPrice(10, 20, 40) // values in gwei
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	price, err := gp.ForLevel(gas.PriceLevelFast)
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println("fast gas price (wei):", price)
package gas
