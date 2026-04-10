package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestCreatePoolSessionBead_SetsPendingCreateClaim(t *testing.T) {
	store := beads.NewMemStore()

	bead, err := createPoolSessionBead(store, "gascity/claude", nil)
	if err != nil {
		t.Fatalf("createPoolSessionBead: %v", err)
	}

	if got := bead.Metadata["pending_create_claim"]; got != "true" {
		t.Fatalf("pending_create_claim = %q, want true", got)
	}
	if got := bead.Metadata["state"]; got != "creating" {
		t.Fatalf("state = %q, want creating", got)
	}
	if got := bead.Metadata["session_name"]; got == "" {
		t.Fatal("session_name should be populated")
	}
}
