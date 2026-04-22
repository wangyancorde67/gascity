package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestWritePreLaunchClaimResultDrain(t *testing.T) {
	var out bytes.Buffer
	if err := writePreLaunchClaimResult(&out, claimNextResult{Reason: "no_work"}); err != nil {
		t.Fatalf("writePreLaunchClaimResult: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got["action"] != "drain" || got["reason"] != "no_work" {
		t.Fatalf("response = %#v, want drain/no_work", got)
	}
}

func TestWritePreLaunchClaimResultContinue(t *testing.T) {
	var out bytes.Buffer
	if err := writePreLaunchClaimResult(&out, claimNextResult{
		Reason: "claimed",
		Bead:   beads.Bead{ID: "ga-123"},
	}); err != nil {
		t.Fatalf("writePreLaunchClaimResult: %v", err)
	}
	var got struct {
		Action      string            `json:"action"`
		Reason      string            `json:"reason"`
		Env         map[string]string `json:"env"`
		NudgeAppend string            `json:"nudge_append"`
		Metadata    map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Action != "continue" || got.Env["GC_WORK_BEAD"] != "ga-123" {
		t.Fatalf("response = %#v, want continue with GC_WORK_BEAD", got)
	}
	if got.Metadata["pre_launch.user.claimed_work_bead"] != "ga-123" {
		t.Fatalf("metadata = %#v, want claimed work bead", got.Metadata)
	}
}

func TestClaimNextWorkReturnsExistingInProgressAssignment(t *testing.T) {
	store := beads.NewMemStore()
	assigned, err := store.Create(beads.Bead{Title: "existing assignment"})
	if err != nil {
		t.Fatalf("Create(existing): %v", err)
	}
	status := "in_progress"
	assignee := "sess-1"
	if err := store.Update(assigned.ID, beads.UpdateOpts{
		Status:   &status,
		Assignee: &assignee,
	}); err != nil {
		t.Fatalf("Update(existing): %v", err)
	}

	result, err := claimNextWork(context.Background(), store, t.TempDir(), "mayor", "sess-1", []string{"sess-1"})
	if err != nil {
		t.Fatalf("claimNextWork(existing): %v", err)
	}
	if result.Bead.ID != assigned.ID {
		t.Fatalf("claimNextWork(existing) bead = %q, want %q", result.Bead.ID, assigned.ID)
	}
	if result.Reason != "existing_assignment" {
		t.Fatalf("claimNextWork(existing) reason = %q, want existing_assignment", result.Reason)
	}
}

func TestClaimNextWorkReturnsAssignedReadyBeforeClaimingNewWork(t *testing.T) {
	store := beads.NewMemStore()
	assigned, err := store.Create(beads.Bead{Title: "ready assigned"})
	if err != nil {
		t.Fatalf("Create(assigned): %v", err)
	}
	readyAssignee := "alias-1"
	if err := store.Update(assigned.ID, beads.UpdateOpts{Assignee: &readyAssignee}); err != nil {
		t.Fatalf("Update(assigned): %v", err)
	}
	routed, err := store.Create(beads.Bead{
		Title:    "unassigned routed",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})
	if err != nil {
		t.Fatalf("Create(routed): %v", err)
	}

	oldClaimWork := claimWork
	claimWork = func(context.Context, string, string, string) error {
		t.Fatalf("claimWork called unexpectedly with routed bead %q", routed.ID)
		return nil
	}
	t.Cleanup(func() { claimWork = oldClaimWork })

	result, err := claimNextWork(context.Background(), store, t.TempDir(), "mayor", "sess-1", []string{"sess-1", "alias-1"})
	if err != nil {
		t.Fatalf("claimNextWork(assigned ready): %v", err)
	}
	if result.Bead.ID != assigned.ID {
		t.Fatalf("claimNextWork(assigned ready) bead = %q, want %q", result.Bead.ID, assigned.ID)
	}
	if result.Reason != "ready_assignment" {
		t.Fatalf("claimNextWork(assigned ready) reason = %q, want ready_assignment", result.Reason)
	}
}

func TestClaimNextWorkReturnsLegacyWorkflowControlAssignment(t *testing.T) {
	store := beads.NewMemStore()
	assigned, err := store.Create(beads.Bead{Title: "legacy ready assigned"})
	if err != nil {
		t.Fatalf("Create(assigned): %v", err)
	}
	readyAssignee := "gascity--workflow-control"
	if err := store.Update(assigned.ID, beads.UpdateOpts{Assignee: &readyAssignee}); err != nil {
		t.Fatalf("Update(assigned): %v", err)
	}

	result, err := claimNextWork(
		context.Background(),
		store,
		t.TempDir(),
		"gascity/control-dispatcher",
		"gascity--control-dispatcher",
		[]string{"gascity--control-dispatcher"},
	)
	if err != nil {
		t.Fatalf("claimNextWork(legacy assigned): %v", err)
	}
	if result.Bead.ID != assigned.ID {
		t.Fatalf("claimNextWork(legacy assigned) bead = %q, want %q", result.Bead.ID, assigned.ID)
	}
	if result.Reason != "ready_assignment" {
		t.Fatalf("claimNextWork(legacy assigned) reason = %q, want ready_assignment", result.Reason)
	}
}

func TestClaimNextWorkClaimsReadyUnassignedRoutedWork(t *testing.T) {
	store := beads.NewMemStore()
	blocker, err := store.Create(beads.Bead{Title: "blocker"})
	if err != nil {
		t.Fatalf("Create(blocker): %v", err)
	}
	blocked, err := store.Create(beads.Bead{
		Title:    "blocked routed",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})
	if err != nil {
		t.Fatalf("Create(blocked): %v", err)
	}
	if err := store.DepAdd(blocked.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd(blocked): %v", err)
	}
	routed, err := store.Create(beads.Bead{
		Title:    "ready routed",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})
	if err != nil {
		t.Fatalf("Create(routed): %v", err)
	}

	oldClaimWork := claimWork
	claimWork = func(ctx context.Context, dir, beadID, assignee string) error {
		if beadID != routed.ID {
			t.Fatalf("claimWork bead = %q, want %q", beadID, routed.ID)
		}
		inProgress := "in_progress"
		return store.Update(beadID, beads.UpdateOpts{
			Status:   &inProgress,
			Assignee: &assignee,
		})
	}
	t.Cleanup(func() { claimWork = oldClaimWork })

	result, err := claimNextWork(context.Background(), store, t.TempDir(), "mayor", "sess-1", []string{"sess-1"})
	if err != nil {
		t.Fatalf("claimNextWork(routed): %v", err)
	}
	if result.Bead.ID != routed.ID {
		t.Fatalf("claimNextWork(routed) bead = %q, want %q", result.Bead.ID, routed.ID)
	}
	if result.Reason != "claimed" {
		t.Fatalf("claimNextWork(routed) reason = %q, want claimed", result.Reason)
	}
	gotBlocked, err := store.Get(blocked.ID)
	if err != nil {
		t.Fatalf("Get(blocked): %v", err)
	}
	if gotBlocked.Assignee != "" {
		t.Fatalf("blocked bead assignee = %q, want empty", gotBlocked.Assignee)
	}
}

func TestClaimNextWorkClaimsLegacyWorkflowControlRoute(t *testing.T) {
	store := beads.NewMemStore()
	routed, err := store.Create(beads.Bead{
		Title:    "legacy routed",
		Metadata: map[string]string{"gc.routed_to": "gascity/workflow-control"},
	})
	if err != nil {
		t.Fatalf("Create(routed): %v", err)
	}

	oldClaimWork := claimWork
	claimWork = func(ctx context.Context, dir, beadID, assignee string) error {
		if beadID != routed.ID {
			t.Fatalf("claimWork bead = %q, want %q", beadID, routed.ID)
		}
		inProgress := "in_progress"
		return store.Update(beadID, beads.UpdateOpts{
			Status:   &inProgress,
			Assignee: &assignee,
		})
	}
	t.Cleanup(func() { claimWork = oldClaimWork })

	result, err := claimNextWork(
		context.Background(),
		store,
		t.TempDir(),
		"gascity/control-dispatcher",
		"gascity--control-dispatcher",
		[]string{"gascity--control-dispatcher"},
	)
	if err != nil {
		t.Fatalf("claimNextWork(legacy route): %v", err)
	}
	if result.Bead.ID != routed.ID {
		t.Fatalf("claimNextWork(legacy route) bead = %q, want %q", result.Bead.ID, routed.ID)
	}
	if result.Reason != "claimed" {
		t.Fatalf("claimNextWork(legacy route) reason = %q, want claimed", result.Reason)
	}
}

func TestClaimNextWorkRetriesAfterClaimConflict(t *testing.T) {
	store := beads.NewMemStore()
	first, err := store.Create(beads.Bead{
		Title:    "first routed",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})
	if err != nil {
		t.Fatalf("Create(first): %v", err)
	}
	second, err := store.Create(beads.Bead{
		Title:    "second routed",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})
	if err != nil {
		t.Fatalf("Create(second): %v", err)
	}

	oldClaimWork := claimWork
	claimAttempts := 0
	claimWork = func(ctx context.Context, dir, beadID, assignee string) error {
		claimAttempts++
		if claimAttempts == 1 {
			return beads.ErrClaimConflict
		}
		inProgress := "in_progress"
		return store.Update(beadID, beads.UpdateOpts{
			Status:   &inProgress,
			Assignee: &assignee,
		})
	}
	t.Cleanup(func() { claimWork = oldClaimWork })

	result, err := claimNextWork(context.Background(), store, t.TempDir(), "mayor", "sess-1", []string{"sess-1"})
	if err != nil {
		t.Fatalf("claimNextWork(conflict): %v", err)
	}
	if claimAttempts != 2 {
		t.Fatalf("claimAttempts = %d, want 2", claimAttempts)
	}
	if result.Bead.ID != second.ID {
		t.Fatalf("claimNextWork(conflict) bead = %q, want %q", result.Bead.ID, second.ID)
	}
	if result.Bead.ID == first.ID {
		t.Fatalf("claimNextWork(conflict) returned first conflicted bead %q", first.ID)
	}
}

func TestClaimNextWorkFallsBackToOpenRoutedMoleculeRoot(t *testing.T) {
	store := beads.NewMemStore()
	molecule, err := store.Create(beads.Bead{
		Title:    "molecule root",
		Type:     "molecule",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})
	if err != nil {
		t.Fatalf("Create(molecule): %v", err)
	}

	oldClaimWork := claimWork
	claimWork = func(ctx context.Context, dir, beadID, assignee string) error {
		inProgress := "in_progress"
		return store.Update(beadID, beads.UpdateOpts{
			Status:   &inProgress,
			Assignee: &assignee,
		})
	}
	t.Cleanup(func() { claimWork = oldClaimWork })

	result, err := claimNextWork(context.Background(), store, t.TempDir(), "mayor", "sess-1", []string{"sess-1"})
	if err != nil {
		t.Fatalf("claimNextWork(molecule): %v", err)
	}
	if result.Bead.ID != molecule.ID {
		t.Fatalf("claimNextWork(molecule) bead = %q, want %q", result.Bead.ID, molecule.ID)
	}
	if result.Reason != "claimed_molecule" {
		t.Fatalf("claimNextWork(molecule) reason = %q, want claimed_molecule", result.Reason)
	}
}

func TestWorkClaimNextCmdUsesStoreScopeRoot(t *testing.T) {
	oldResolveCity := resolveWorkCity
	oldOpenStoreAt := openWorkStoreAt
	oldClaimNextWork := claimNextWorkFn
	t.Cleanup(func() {
		resolveWorkCity = oldResolveCity
		openWorkStoreAt = oldOpenStoreAt
		claimNextWorkFn = oldClaimNextWork
	})

	resolveWorkCity = func() (string, error) {
		return "/tmp/test-city", nil
	}

	var gotStoreRoot, gotCityPath string
	openWorkStoreAt = func(storeRoot, cityPath string) (beads.Store, error) {
		gotStoreRoot = storeRoot
		gotCityPath = cityPath
		return beads.NewMemStore(), nil
	}
	claimNextWorkFn = func(ctx context.Context, store beads.Store, claimDir, template, assignee string, identities []string) (claimNextResult, error) {
		if claimDir != "/tmp/test-rig" {
			t.Fatalf("claimDir = %q, want /tmp/test-rig", claimDir)
		}
		if template != "mayor" {
			t.Fatalf("template = %q, want mayor", template)
		}
		if assignee != "sess-1" {
			t.Fatalf("assignee = %q, want sess-1", assignee)
		}
		return claimNextResult{Reason: "no_work"}, nil
	}

	t.Setenv("GC_STORE_ROOT", "/tmp/test-rig")
	t.Setenv("GC_TEMPLATE", "mayor")
	t.Setenv("GC_SESSION_ID", "sess-1")

	var stdout, stderr bytes.Buffer
	cmd := newWorkClaimNextCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if gotStoreRoot != "/tmp/test-rig" {
		t.Fatalf("openWorkStoreAt storeRoot = %q, want /tmp/test-rig", gotStoreRoot)
	}
	if gotCityPath != "/tmp/test-city" {
		t.Fatalf("openWorkStoreAt cityPath = %q, want /tmp/test-city", gotCityPath)
	}
	if got := stdout.String(); got == "" {
		t.Fatal("stdout empty, want JSON output")
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout = %q, want valid JSON: %v", stdout.String(), err)
	}
}
