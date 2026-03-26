package workflow

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/formula"
)

func TestProcessRetryControlPass(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "review",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.max_attempts":     "3",
			"gc.on_exhausted":     "hard_fail",
			"gc.source_step_spec": `{"id":"review","title":"Review","type":"task","retry":{"max_attempts":3}}`,
			"gc.control_epoch":    "1",
		},
	})
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "review attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
			"gc.attempt":      "1",
			"gc.outcome":      "pass",
			"gc.output_json":  `{"ok":true}`,
			"review.verdict":  "approved",
		},
	})
	mustClose(t, store, attempt1.ID)
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	result, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{})
	if err != nil {
		t.Fatalf("processRetryControl: %v", err)
	}
	if !result.Processed || result.Action != "pass" {
		t.Fatalf("result = %+v, want processed pass", result)
	}

	after := mustGet(t, store, control.ID)
	if after.Status != "closed" {
		t.Fatalf("control status = %q, want closed", after.Status)
	}
	if after.Metadata["gc.outcome"] != "pass" {
		t.Fatalf("control outcome = %q, want pass", after.Metadata["gc.outcome"])
	}
	if after.Metadata["gc.output_json"] != `{"ok":true}` {
		t.Fatalf("control output_json = %q, want propagated", after.Metadata["gc.output_json"])
	}
	if after.Metadata["review.verdict"] != "approved" {
		t.Fatalf("control review.verdict = %q, want approved", after.Metadata["review.verdict"])
	}
}

func TestProcessRetryControlHardFail(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "review",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.max_attempts":     "3",
			"gc.on_exhausted":     "hard_fail",
			"gc.source_step_spec": `{"id":"review","title":"Review","type":"task","retry":{"max_attempts":3}}`,
			"gc.control_epoch":    "1",
		},
	})
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "review attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id":   root.ID,
			"gc.attempt":        "1",
			"gc.outcome":        "fail",
			"gc.failure_class":  "hard",
			"gc.failure_reason": "auth_error",
		},
	})
	mustClose(t, store, attempt1.ID)
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	result, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{})
	if err != nil {
		t.Fatalf("processRetryControl: %v", err)
	}
	if result.Action != "hard-fail" {
		t.Fatalf("action = %q, want hard-fail", result.Action)
	}

	after := mustGet(t, store, control.ID)
	if after.Status != "closed" || after.Metadata["gc.outcome"] != "fail" {
		t.Fatalf("control = status %q outcome %q, want closed/fail", after.Status, after.Metadata["gc.outcome"])
	}
	if after.Metadata["gc.final_disposition"] != "hard_fail" {
		t.Fatalf("disposition = %q, want hard_fail", after.Metadata["gc.final_disposition"])
	}
}

func TestProcessRetryControlTransientRetry(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "review",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.max_attempts":     "3",
			"gc.on_exhausted":     "hard_fail",
			"gc.source_step_spec": `{"id":"review","title":"Review","type":"task","retry":{"max_attempts":3}}`,
			"gc.control_epoch":    "1",
		},
	})
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "review attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id":   root.ID,
			"gc.attempt":        "1",
			"gc.outcome":        "fail",
			"gc.failure_class":  "transient",
			"gc.failure_reason": "rate_limited",
		},
	})
	mustClose(t, store, attempt1.ID)
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	result, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{})
	if err != nil {
		t.Fatalf("processRetryControl: %v", err)
	}
	if result.Action != "retry" {
		t.Fatalf("action = %q, want retry", result.Action)
	}

	// Control bead should still be open (waiting on attempt 2).
	after := mustGet(t, store, control.ID)
	if after.Status != "open" {
		t.Fatalf("control status = %q, want open (blocking on attempt 2)", after.Status)
	}

	// Should have a new blocking dep (attempt 2).
	deps, _ := store.DepList(control.ID, "down")
	if len(deps) < 2 {
		t.Fatalf("control deps = %d, want >= 2 (attempt 1 + attempt 2)", len(deps))
	}

	// Epoch should have advanced.
	if after.Metadata["gc.control_epoch"] != "2" {
		t.Fatalf("epoch = %q, want 2", after.Metadata["gc.control_epoch"])
	}

	// Attempt log should record the decision.
	var log []map[string]string
	if err := json.Unmarshal([]byte(after.Metadata["gc.attempt_log"]), &log); err != nil {
		t.Fatalf("unmarshal attempt_log: %v", err)
	}
	if len(log) != 1 || log[0]["outcome"] != "transient" {
		t.Fatalf("attempt_log = %v, want [{attempt:1 outcome:transient}]", log)
	}
}

func TestProcessRetryControlSoftFailOnExhaustion(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "review",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.max_attempts":     "1",
			"gc.on_exhausted":     "soft_fail",
			"gc.source_step_spec": `{"id":"review","title":"Review","type":"task","retry":{"max_attempts":1,"on_exhausted":"soft_fail"}}`,
			"gc.control_epoch":    "1",
		},
	})
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "review attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id":   root.ID,
			"gc.attempt":        "1",
			"gc.outcome":        "fail",
			"gc.failure_class":  "transient",
			"gc.failure_reason": "rate_limited",
		},
	})
	mustClose(t, store, attempt1.ID)
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	result, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{})
	if err != nil {
		t.Fatalf("processRetryControl: %v", err)
	}
	if result.Action != "soft-fail" {
		t.Fatalf("action = %q, want soft-fail", result.Action)
	}

	after := mustGet(t, store, control.ID)
	if after.Status != "closed" || after.Metadata["gc.outcome"] != "pass" {
		t.Fatalf("control = status %q outcome %q, want closed/pass (soft-fail closes as pass)", after.Status, after.Metadata["gc.outcome"])
	}
	if after.Metadata["gc.final_disposition"] != "soft_fail" {
		t.Fatalf("disposition = %q, want soft_fail", after.Metadata["gc.final_disposition"])
	}
}

func TestProcessRetryControlInvariantViolation(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "review",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.max_attempts":     "3",
			"gc.source_step_spec": `{"id":"review","title":"Review","type":"task"}`,
			"gc.control_epoch":    "1",
		},
	})
	// Attempt is still open — control should not be processing.
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "review attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
			"gc.attempt":      "1",
		},
	})
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	_, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{})
	if err == nil {
		t.Fatal("expected invariant violation error")
	}
	if !contains(err.Error(), "invariant violation") {
		t.Fatalf("error = %v, want invariant violation", err)
	}
}

// Test helpers (specific to control_test.go to avoid conflicts with runtime_test.go helpers).

func mustCreate(t *testing.T, store beads.Store, b beads.Bead) beads.Bead {
	t.Helper()
	created, err := store.Create(b)
	if err != nil {
		t.Fatalf("create %q: %v", b.Title, err)
	}
	for k, v := range b.Metadata {
		if created.Metadata[k] != v {
			if err := store.SetMetadata(created.ID, k, v); err != nil {
				t.Fatalf("set metadata %s=%s: %v", k, v, err)
			}
		}
	}
	created, _ = store.Get(created.ID)
	return created
}

func mustClose(t *testing.T, store beads.Store, id string) {
	t.Helper()
	if err := store.Close(id); err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
}

func mustDep(t *testing.T, store beads.Store, from, to, depType string) {
	t.Helper()
	if err := store.DepAdd(from, to, depType); err != nil {
		t.Fatalf("dep %s -> %s: %v", from, to, err)
	}
}

func mustGet(t *testing.T, store beads.Store, id string) beads.Bead {
	t.Helper()
	b, err := store.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return b
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Verify that attempt_log is recorded correctly across multiple test scenarios.
func TestAttemptLogRecording(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	bead, _ := store.Create(beads.Bead{Title: "test", Metadata: map[string]string{}})

	if err := appendAttemptLog(store, bead.ID, 1, "transient", "rate_limited"); err != nil {
		t.Fatalf("appendAttemptLog 1: %v", err)
	}
	if err := appendAttemptLog(store, bead.ID, 2, "pass", ""); err != nil {
		t.Fatalf("appendAttemptLog 2: %v", err)
	}

	after, _ := store.Get(bead.ID)
	var log []map[string]string
	if err := json.Unmarshal([]byte(after.Metadata["gc.attempt_log"]), &log); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(log) != 2 {
		t.Fatalf("log entries = %d, want 2", len(log))
	}
	if log[0]["attempt"] != "1" || log[0]["outcome"] != "transient" || log[0]["action"] != "retry" {
		t.Errorf("log[0] = %v, want attempt:1 outcome:transient action:retry", log[0])
	}
	if log[1]["attempt"] != "2" || log[1]["outcome"] != "pass" || log[1]["action"] != "close" {
		t.Errorf("log[1] = %v, want attempt:2 outcome:pass action:close", log[1])
	}
}

func TestBuildAttemptRecipe(t *testing.T) {
	step := &formula.Step{
		ID:     "review",
		Title:  "Review code",
		Type:   "task",
		Labels: []string{"pool:polecat"},
		Retry:  &formula.RetrySpec{MaxAttempts: 3},
	}

	control := beads.Bead{
		ID: "gc-1",
		Metadata: map[string]string{
			"gc.step_id": "review",
		},
	}

	recipe := buildAttemptRecipe(step, control, 2)

	if recipe.Name != "review.attempt.2" {
		t.Errorf("recipe name = %q, want review.attempt.2", recipe.Name)
	}
	if len(recipe.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 (simple retry has one step)", len(recipe.Steps))
	}
	if recipe.Steps[0].ID != "review.attempt.2" {
		t.Errorf("step ID = %q, want review.attempt.2", recipe.Steps[0].ID)
	}
	if recipe.Steps[0].Metadata["gc.attempt"] != "2" {
		t.Errorf("gc.attempt = %q, want 2", recipe.Steps[0].Metadata["gc.attempt"])
	}
}

func TestBuildAttemptRecipeWithChildren(t *testing.T) {
	step := &formula.Step{
		ID:    "converge",
		Title: "Converge",
		Type:  "task",
		Ralph: &formula.RalphSpec{MaxAttempts: 5},
		Children: []*formula.Step{
			{ID: "apply", Title: "Apply", Type: "task"},
			{ID: "verify", Title: "Verify", Type: "task", Needs: []string{"apply"}},
		},
	}

	control := beads.Bead{
		ID: "gc-1",
		Metadata: map[string]string{
			"gc.step_id": "converge",
		},
	}

	recipe := buildAttemptRecipe(step, control, 3)

	if recipe.Name != "converge.iteration.3" {
		t.Errorf("recipe name = %q, want converge.iteration.3", recipe.Name)
	}
	if len(recipe.Steps) != 3 {
		t.Fatalf("steps = %d, want 3 (root + 2 children)", len(recipe.Steps))
	}

	// Root
	if recipe.Steps[0].ID != "converge.iteration.3" {
		t.Errorf("root ID = %q", recipe.Steps[0].ID)
	}
	// Children
	if recipe.Steps[1].ID != "converge.iteration.3.apply" {
		t.Errorf("child 1 ID = %q", recipe.Steps[1].ID)
	}
	if recipe.Steps[2].ID != "converge.iteration.3.verify" {
		t.Errorf("child 2 ID = %q", recipe.Steps[2].ID)
	}

	// verify should block on apply (namespaced).
	foundDep := false
	for _, dep := range recipe.Deps {
		if dep.StepID == "converge.iteration.3.verify" && dep.DependsOnID == "converge.iteration.3.apply" && dep.Type == "blocks" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Errorf("missing dep: verify blocks on apply; deps = %+v", recipe.Deps)
	}
}

func TestFindLatestAttemptNestedRetryInsideRalph(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})

	// Retry control inside a ralph iteration — step_ref is fully namespaced
	control := mustCreate(t, store, beads.Bead{
		Title: "review-own-code retry",
		Metadata: map[string]string{
			"gc.kind":         "retry",
			"gc.root_bead_id": root.ID,
			"gc.step_ref":     "mol-demo-v2.self-review.iteration.1.review-own-code",
			"gc.step_id":      "self-review",
		},
	})

	// Attempt bead — step_ref is SHORT (bare child ID, not fully namespaced)
	attempt := mustCreate(t, store, beads.Bead{
		Title: "review-own-code attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
			"gc.step_ref":     "review-own-code.attempt.1",
			"gc.attempt":      "1",
		},
	})
	mustClose(t, store, attempt.ID)

	// Scope-check (should be skipped by findLatestAttempt)
	scopeCheck := mustCreate(t, store, beads.Bead{
		Title: "scope-check for attempt",
		Metadata: map[string]string{
			"gc.kind":         "scope-check",
			"gc.root_bead_id": root.ID,
			"gc.attempt":      "1",
		},
	})
	mustClose(t, store, scopeCheck.ID)
	mustDep(t, store, control.ID, scopeCheck.ID, "blocks")

	found, err := findLatestAttempt(store, mustGet(t, store, control.ID))
	if err != nil {
		t.Fatalf("findLatestAttempt: %v", err)
	}
	if found.ID != attempt.ID {
		t.Fatalf("findLatestAttempt returned %q, want %q (attempt bead)", found.ID, attempt.ID)
	}
}

func TestFindLatestAttemptDirectRef(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})

	control := mustCreate(t, store, beads.Bead{
		Title: "review retry",
		Metadata: map[string]string{
			"gc.kind":         "retry",
			"gc.root_bead_id": root.ID,
			"gc.step_ref":     "mol-feature.review",
			"gc.step_id":      "review",
		},
	})

	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
			"gc.step_ref":     "mol-feature.review.attempt.1",
			"gc.attempt":      "1",
		},
	})
	mustClose(t, store, attempt1.ID)

	attempt2 := mustCreate(t, store, beads.Bead{
		Title: "attempt 2",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
			"gc.step_ref":     "mol-feature.review.attempt.2",
			"gc.attempt":      "2",
		},
	})
	mustClose(t, store, attempt2.ID)

	found, err := findLatestAttempt(store, mustGet(t, store, control.ID))
	if err != nil {
		t.Fatalf("findLatestAttempt: %v", err)
	}
	if found.ID != attempt2.ID {
		t.Fatalf("findLatestAttempt returned %q, want %q (latest attempt)", found.ID, attempt2.ID)
	}
}

// Unused import guard.
var _ = strconv.Itoa
