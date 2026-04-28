package api

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestReassignOpenWorkAssignedToSession_UsesLiveOpenOwnership(t *testing.T) {
	t.Parallel()

	backing := beads.NewMemStore()
	work, err := backing.Create(beads.Bead{
		Title:    "open work",
		Type:     "task",
		Status:   "open",
		Assignee: "retired-session",
	})
	if err != nil {
		t.Fatalf("Create(work): %v", err)
	}

	cache := beads.NewCachingStoreForTest(backing, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	reassigned := "replacement-picked-elsewhere"
	if err := backing.Update(work.ID, beads.UpdateOpts{Assignee: &reassigned}); err != nil {
		t.Fatalf("Update(%s, reassigned): %v", work.ID, err)
	}

	if err := reassignOpenWorkAssignedToSession(cache, beads.Bead{ID: "retired-session"}, "new-canonical"); err != nil {
		t.Fatalf("reassignOpenWorkAssignedToSession: %v", err)
	}

	got, err := backing.Get(work.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", work.ID, err)
	}
	if got.Assignee != reassigned {
		t.Fatalf("Assignee = %q, want %q; stale open ownership should not be overwritten", got.Assignee, reassigned)
	}
}

func TestReassignOpenWorkAssignedToSessionMatchesSessionNameAssignee(t *testing.T) {
	t.Parallel()

	store := beads.NewMemStore()
	work, err := store.Create(beads.Bead{
		Title:    "open work",
		Type:     "task",
		Status:   "open",
		Assignee: "runtime-session-name",
	})
	if err != nil {
		t.Fatalf("Create(work): %v", err)
	}

	err = reassignOpenWorkAssignedToSession(store, beads.Bead{
		ID: "retired-session",
		Metadata: map[string]string{
			"session_name": "runtime-session-name",
		},
	}, "new-canonical")
	if err != nil {
		t.Fatalf("reassignOpenWorkAssignedToSession: %v", err)
	}

	got, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", work.ID, err)
	}
	if got.Assignee != "new-canonical" {
		t.Fatalf("Assignee = %q, want new-canonical", got.Assignee)
	}
}

func TestReassignContinuityIneligibleNamedSessionStateReassignsRigStores(t *testing.T) {
	t.Parallel()

	state := newFakeState(t)
	cityStore := beads.NewMemStore()
	rigStore := beads.NewMemStore()
	state.cityBeadStore = cityStore
	state.stores = map[string]beads.Store{"myrig": rigStore}
	srv := &Server{state: state}

	work, err := rigStore.Create(beads.Bead{
		Title:    "rig work",
		Type:     "task",
		Status:   "open",
		Assignee: "runtime-session-name",
	})
	if err != nil {
		t.Fatalf("Create(work): %v", err)
	}
	retired := beads.Bead{
		ID: "retired-session",
		Metadata: map[string]string{
			"session_name": "runtime-session-name",
		},
	}

	if err := srv.reassignContinuityIneligibleNamedSessionState(context.Background(), cityStore, []beads.Bead{retired}, "new-canonical"); err != nil {
		t.Fatalf("reassignContinuityIneligibleNamedSessionState: %v", err)
	}

	got, err := rigStore.Get(work.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", work.ID, err)
	}
	if got.Assignee != "new-canonical" {
		t.Fatalf("Assignee = %q, want new-canonical", got.Assignee)
	}
}
