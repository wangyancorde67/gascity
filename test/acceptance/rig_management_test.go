//go:build acceptance_a

// Rig management acceptance tests.
//
// These exercise the real gc binary's rig subcommands (add, list, suspend,
// resume) as a black box. All tests use the subprocess session provider and
// file beads — no tmux, no dolt, no inference. No running supervisor is
// needed; rig commands fall through to direct city.toml mutation when the
// API server is unavailable.
package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// --- gc rig add ---

// TestRigAdd_NewRig_AppendsToConfig verifies that adding a new rig via
// gc rig add updates city.toml with a [[rigs]] entry and reports success.
func TestRigAdd_NewRig_AppendsToConfig(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	rigPath := filepath.Join(t.TempDir(), "my-frontend")
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := c.GC("rig", "add", rigPath)
	if err != nil {
		t.Fatalf("gc rig add failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Adding rig 'my-frontend'") {
		t.Errorf("expected 'Adding rig' message, got:\n%s", out)
	}
	if !strings.Contains(out, "Rig added.") {
		t.Errorf("expected 'Rig added.' message, got:\n%s", out)
	}

	// Verify city.toml contains the rig.
	toml := c.ReadFile("city.toml")
	if !strings.Contains(toml, "my-frontend") {
		t.Errorf("city.toml should contain rig name 'my-frontend':\n%s", toml)
	}
}

// TestRigAdd_CreatesNonexistentDir verifies that gc rig add creates the
// target directory if it doesn't exist.
func TestRigAdd_CreatesNonexistentDir(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	rigPath := filepath.Join(t.TempDir(), "nonexistent-project")

	out, err := c.GC("rig", "add", rigPath)
	if err != nil {
		t.Fatalf("gc rig add failed: %v\n%s", err, out)
	}

	fi, err := os.Stat(rigPath)
	if err != nil {
		t.Fatalf("rig directory not created: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("rig path exists but is not a directory")
	}
}

// TestRigAdd_MissingPath_ReturnsError verifies that gc rig add with no
// path argument exits with an error.
func TestRigAdd_MissingPath_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "add")
	if err == nil {
		t.Fatal("expected error for missing path, got success")
	}
	if !strings.Contains(out, "missing path") {
		t.Errorf("expected 'missing path' error, got:\n%s", out)
	}
}

// TestRigAdd_DuplicateName_DifferentPath_ReturnsError verifies that adding
// a rig with the same name as an existing rig at a different path fails.
func TestRigAdd_DuplicateName_DifferentPath_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Add the first rig.
	rigPath1 := filepath.Join(t.TempDir(), "myrig")
	if err := os.MkdirAll(rigPath1, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := c.GC("rig", "add", rigPath1)
	if err != nil {
		t.Fatalf("first rig add failed: %v\n%s", err, out)
	}

	// Add a second rig with the same base name at a different path.
	rigPath2 := filepath.Join(t.TempDir(), "myrig")
	if err := os.MkdirAll(rigPath2, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = c.GC("rig", "add", rigPath2)
	if err == nil {
		t.Fatal("expected error for duplicate rig name at different path, got success")
	}
	if !strings.Contains(out, "already registered") {
		t.Errorf("expected 'already registered' error, got:\n%s", out)
	}
}

// TestRigAdd_ReAdd_SamePath_Succeeds verifies that re-adding an existing
// rig at the same path re-initializes it without error.
func TestRigAdd_ReAdd_SamePath_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	rigPath := filepath.Join(t.TempDir(), "my-service")
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// First add.
	out, err := c.GC("rig", "add", rigPath)
	if err != nil {
		t.Fatalf("first rig add failed: %v\n%s", err, out)
	}

	// Re-add at the same path.
	out, err = c.GC("rig", "add", rigPath)
	if err != nil {
		t.Fatalf("re-add failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Re-initializing") {
		t.Errorf("expected 'Re-initializing' message, got:\n%s", out)
	}
	if !strings.Contains(out, "Rig re-initialized.") {
		t.Errorf("expected 'Rig re-initialized.' message, got:\n%s", out)
	}
}

// TestRigAdd_StartSuspended_SetsSuspendedInConfig verifies that --start-suspended
// adds the rig in a suspended state.
func TestRigAdd_StartSuspended_SetsSuspendedInConfig(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	rigPath := filepath.Join(t.TempDir(), "dormant-project")
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := c.GC("rig", "add", "--start-suspended", rigPath)
	if err != nil {
		t.Fatalf("gc rig add --start-suspended failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "suspended") {
		t.Errorf("expected 'suspended' in output, got:\n%s", out)
	}

	// Verify city.toml has suspended = true.
	toml := c.ReadFile("city.toml")
	if !strings.Contains(toml, "suspended") {
		t.Errorf("city.toml should contain 'suspended' for the rig:\n%s", toml)
	}
}

// --- gc rig list ---

// TestRigList_EmptyCity_ShowsOnlyHQ verifies that gc rig list on a city
// with no configured rigs shows only the HQ rig.
func TestRigList_EmptyCity_ShowsOnlyHQ(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "list")
	if err != nil {
		t.Fatalf("gc rig list failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "(HQ)") {
		t.Errorf("expected HQ rig in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Prefix:") {
		t.Errorf("expected prefix line in output, got:\n%s", out)
	}
}

// TestRigList_WithRigs_ShowsAllRigs verifies that gc rig list shows both
// HQ and configured rigs with their paths and prefixes.
func TestRigList_WithRigs_ShowsAllRigs(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Add two rigs.
	rig1 := filepath.Join(t.TempDir(), "alpha")
	rig2 := filepath.Join(t.TempDir(), "beta")
	for _, p := range []string{rig1, rig2} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out, err := c.GC("rig", "add", rig1)
	if err != nil {
		t.Fatalf("rig add alpha: %v\n%s", err, out)
	}
	out, err = c.GC("rig", "add", rig2)
	if err != nil {
		t.Fatalf("rig add beta: %v\n%s", err, out)
	}

	out, err = c.GC("rig", "list")
	if err != nil {
		t.Fatalf("gc rig list failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "(HQ)") {
		t.Errorf("expected HQ rig, got:\n%s", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected 'alpha' rig, got:\n%s", out)
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("expected 'beta' rig, got:\n%s", out)
	}
}

// --- gc rig suspend / resume ---

// TestRigSuspend_ThenResume_TogglesState verifies the full suspend/resume
// cycle: add a rig, suspend it, verify it appears suspended in rig list,
// resume it, verify it no longer appears suspended.
func TestRigSuspend_ThenResume_TogglesState(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	rigPath := filepath.Join(t.TempDir(), "togglerig")
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := c.GC("rig", "add", rigPath)
	if err != nil {
		t.Fatalf("rig add: %v\n%s", err, out)
	}

	// Suspend.
	out, err = c.GC("rig", "suspend", "togglerig")
	if err != nil {
		t.Fatalf("rig suspend: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Suspended rig 'togglerig'") {
		t.Errorf("expected suspend confirmation, got:\n%s", out)
	}

	// Verify suspended in rig list.
	out, err = c.GC("rig", "list")
	if err != nil {
		t.Fatalf("rig list after suspend: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(suspended)") {
		t.Errorf("expected '(suspended)' in rig list, got:\n%s", out)
	}

	// Resume.
	out, err = c.GC("rig", "resume", "togglerig")
	if err != nil {
		t.Fatalf("rig resume: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Resumed rig 'togglerig'") {
		t.Errorf("expected resume confirmation, got:\n%s", out)
	}

	// Verify no longer suspended in rig list.
	out, err = c.GC("rig", "list")
	if err != nil {
		t.Fatalf("rig list after resume: %v\n%s", err, out)
	}
	if strings.Contains(out, "(suspended)") {
		t.Errorf("rig should not be suspended after resume:\n%s", out)
	}
}

// TestRigSuspend_UnknownRig_ReturnsError verifies that suspending a
// nonexistent rig returns an error.
func TestRigSuspend_UnknownRig_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "suspend", "nosuchrig")
	if err == nil {
		t.Fatal("expected error for unknown rig, got success")
	}
	if !strings.Contains(out, "nosuchrig") {
		t.Errorf("error should mention the rig name, got:\n%s", out)
	}
}

// TestRigResume_UnknownRig_ReturnsError verifies that resuming a
// nonexistent rig returns an error.
func TestRigResume_UnknownRig_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "resume", "nosuchrig")
	if err == nil {
		t.Fatal("expected error for unknown rig, got success")
	}
	if !strings.Contains(out, "nosuchrig") {
		t.Errorf("error should mention the rig name, got:\n%s", out)
	}
}

// TestRigSuspend_MissingName_ReturnsError verifies that gc rig suspend
// with no rig name returns an error.
func TestRigSuspend_MissingName_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "suspend")
	if err == nil {
		t.Fatal("expected error for missing rig name, got success")
	}
	if !strings.Contains(out, "missing rig name") {
		t.Errorf("expected 'missing rig name' error, got:\n%s", out)
	}
}

// TestRigResume_MissingName_ReturnsError verifies that gc rig resume
// with no rig name returns an error.
func TestRigResume_MissingName_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "resume")
	if err == nil {
		t.Fatal("expected error for missing rig name, got success")
	}
	if !strings.Contains(out, "missing rig name") {
		t.Errorf("expected 'missing rig name' error, got:\n%s", out)
	}
}

// --- gc rig (bare command) ---

// TestRig_NoSubcommand_ReturnsError verifies that gc rig with no
// subcommand prints a helpful error listing available subcommands.
func TestRig_NoSubcommand_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig")
	if err == nil {
		t.Fatal("expected error for bare 'gc rig', got success")
	}
	if !strings.Contains(out, "missing subcommand") {
		t.Errorf("expected 'missing subcommand' message, got:\n%s", out)
	}
}

// TestRig_UnknownSubcommand_ReturnsError verifies that gc rig with an
// unknown subcommand prints a helpful error.
func TestRig_UnknownSubcommand_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("rig", "explode")
	if err == nil {
		t.Fatal("expected error for unknown subcommand, got success")
	}
	if !strings.Contains(out, "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' message, got:\n%s", out)
	}
}
