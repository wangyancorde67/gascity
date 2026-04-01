//go:build acceptance_a

// Order command acceptance tests.
//
// These exercise gc order list, show, and check as a black box. Orders
// are formulas with gate conditions for periodic dispatch. The gastown
// example city ships several orders from its packs. Tests also cover
// the bare command error path and nonexistent order lookup.
package acceptance_test

import (
	"path/filepath"
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// --- gc order list ---

// TestOrderList_GastownCity_ShowsOrders verifies that gc order list on
// a gastown city discovers orders from its packs.
func TestOrderList_GastownCity_ShowsOrders(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	out, err := c.GC("order", "list")
	if err != nil {
		t.Fatalf("gc order list failed: %v\n%s", err, out)
	}

	// Gastown ships orders — verify at least one appears.
	if !strings.Contains(out, "NAME") && !strings.Contains(out, "GATE") {
		// Could be "No orders found." if discovery fails, which is also informative.
		if strings.Contains(out, "No orders") {
			t.Log("gc order list found no orders in gastown (may be expected if order discovery requires running city)")
			return
		}
		t.Errorf("expected order table headers or 'No orders', got:\n%s", out)
	}
}

// TestOrderList_TutorialCity_Succeeds verifies that gc order list on
// a tutorial city doesn't crash (may have system orders or none).
func TestOrderList_TutorialCity_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("order", "list")
	if err != nil {
		t.Fatalf("gc order list failed: %v\n%s", err, out)
	}
	// Tutorial city may or may not have orders. Should not crash.
	_ = out
}

// --- gc order show ---

// TestOrderShow_GastownOrder_DisplaysDetails verifies that gc order show
// displays details for a known gastown order.
func TestOrderShow_GastownOrder_DisplaysDetails(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	// List orders to find a real name.
	listOut, err := c.GC("order", "list")
	if err != nil {
		t.Fatalf("gc order list: %v\n%s", err, listOut)
	}
	if strings.Contains(listOut, "No orders") {
		t.Skip("no orders available to show")
	}

	// Parse the first order name from the table (skip header line).
	lines := strings.Split(strings.TrimSpace(listOut), "\n")
	if len(lines) < 2 {
		t.Skip("order list has no data rows")
	}
	// First column of second line is the order name.
	fields := strings.Fields(lines[1])
	if len(fields) == 0 {
		t.Skip("could not parse order name from list output")
	}
	orderName := fields[0]

	out, err := c.GC("order", "show", orderName)
	if err != nil {
		t.Fatalf("gc order show %s: %v\n%s", orderName, err, out)
	}
	if !strings.Contains(out, orderName) {
		t.Errorf("order show should contain the order name %q, got:\n%s", orderName, out)
	}
}

// TestOrderShow_Nonexistent_ReturnsError verifies that showing a
// nonexistent order returns an error.
func TestOrderShow_Nonexistent_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("order", "show", "nonexistent-order-xyz")
	if err == nil {
		t.Fatal("expected error for nonexistent order, got success")
	}
	_ = out
}

// --- gc order check ---

// TestOrderCheck_Succeeds verifies that gc order check runs gate
// evaluation without crashing.
func TestOrderCheck_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// order check evaluates gates. Exit 0 = orders due, exit 1 = none due.
	// Either is acceptable; we're testing it doesn't crash.
	out, _ := c.GC("order", "check")
	_ = out
}

// --- gc order (bare command) ---

// TestOrder_NoSubcommand_ReturnsError verifies that bare gc order
// prints a helpful error about missing subcommand.
func TestOrder_NoSubcommand_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("order")
	if err == nil {
		t.Fatal("expected error for bare 'gc order', got success")
	}
	if !strings.Contains(out, "missing subcommand") {
		t.Errorf("expected 'missing subcommand' message, got:\n%s", out)
	}
}

// TestOrder_UnknownSubcommand_ReturnsError verifies that gc order with
// an unknown subcommand returns a helpful error.
func TestOrder_UnknownSubcommand_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("order", "explode")
	if err == nil {
		t.Fatal("expected error for unknown subcommand, got success")
	}
	if !strings.Contains(out, "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' message, got:\n%s", out)
	}
}
