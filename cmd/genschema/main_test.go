package main

import "testing"

func TestGeneratedCitySchemaIncludesLegacyOrderOverrideGateAlias(t *testing.T) {
	schema, err := runGenerateCitySchemaForTest()
	if err != nil {
		t.Fatalf("runGenerateCitySchemaForTest: %v", err)
	}

	orderOverride, ok := schema.Definitions["OrderOverride"]
	if !ok {
		t.Fatal("OrderOverride definition missing")
	}
	if orderOverride.Properties == nil {
		t.Fatal("OrderOverride properties missing")
	}
	gate, ok := orderOverride.Properties.Get("gate")
	if !ok {
		t.Fatal("legacy gate alias missing from generated OrderOverride schema")
	}
	deprecated, ok := gate.Extras["deprecated"]
	if !ok || deprecated != true {
		t.Fatalf("legacy gate alias should be marked deprecated in generated schema, got %#v", gate.Extras)
	}
}
