//go:build acceptance_a

// Convoy command acceptance tests.
//
// These exercise gc convoy create, list, status, target, close, check,
// stranded, and land as a black box. Convoys are batch work tracking
// containers that group related issues for coordinated delivery.
package acceptance_test

import (
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// --- gc convoy (bare command) ---

func TestConvoy_NoSubcommand_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy")
	if err == nil {
		t.Fatal("expected error for bare 'gc convoy', got success")
	}
	if !strings.Contains(out, "missing subcommand") {
		t.Errorf("expected 'missing subcommand' message, got:\n%s", out)
	}
}

func TestConvoy_UnknownSubcommand_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "explode")
	if err == nil {
		t.Fatal("expected error for unknown subcommand, got success")
	}
	if !strings.Contains(out, "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' message, got:\n%s", out)
	}
}

// --- gc convoy create ---

func TestConvoyCreate_Basic_OutputsID(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "create", "feature-x")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Created convoy") {
		t.Errorf("expected 'Created convoy' in output, got:\n%s", out)
	}
	if !strings.Contains(out, `"feature-x"`) {
		t.Errorf("expected convoy name in output, got:\n%s", out)
	}
}

func TestConvoyCreate_WithFlags_SetsMetadata(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "create", "flagged-convoy",
		"--owner", "quinn",
		"--merge", "mr",
		"--target", "main",
	)
	if err != nil {
		t.Fatalf("gc convoy create with flags: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Created convoy") {
		t.Errorf("expected 'Created convoy' in output, got:\n%s", out)
	}

	// Verify metadata via status.
	id := parseConvoyID(out)
	if id == "" {
		t.Fatal("could not parse convoy ID from create output")
	}
	status, err := c.GC("convoy", "status", id)
	if err != nil {
		t.Fatalf("gc convoy status %s: %v\n%s", id, err, status)
	}
	for _, want := range []string{"quinn", "mr", "main"} {
		if !strings.Contains(status, want) {
			t.Errorf("convoy status should contain %q, got:\n%s", want, status)
		}
	}
}

func TestConvoyCreate_Owned_SetsLabel(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "create", "owned-convoy", "--owned")
	if err != nil {
		t.Fatalf("gc convoy create --owned: %v\n%s", err, out)
	}

	id := parseConvoyID(out)
	if id == "" {
		t.Fatal("could not parse convoy ID from create output")
	}

	// Owned convoys have the "owned" label visible in status.
	status, err := c.GC("convoy", "status", id)
	if err != nil {
		t.Fatalf("gc convoy status: %v\n%s", err, status)
	}
	if !strings.Contains(status, "owned") {
		t.Errorf("owned convoy status should mention 'owned', got:\n%s", status)
	}
}

func TestConvoyCreate_MissingName_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "create")
	if err == nil {
		t.Fatal("expected error for convoy create without name, got success")
	}
	if !strings.Contains(out, "missing convoy name") {
		t.Errorf("expected 'missing convoy name' error, got:\n%s", out)
	}
}

// --- gc convoy list ---

func TestConvoyList_Empty_ShowsNoConvoys(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "list")
	if err != nil {
		t.Fatalf("gc convoy list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No open convoys") {
		t.Errorf("expected 'No open convoys' on fresh city, got:\n%s", out)
	}
}

func TestConvoyList_AfterCreate_ShowsConvoy(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "listed-convoy")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, createOut)
	}

	out, err := c.GC("convoy", "list")
	if err != nil {
		t.Fatalf("gc convoy list: %v\n%s", err, out)
	}
	if strings.Contains(out, "No open convoys") {
		t.Error("convoy list should show the created convoy, got 'No open convoys'")
	}
	if !strings.Contains(out, "listed-convoy") {
		t.Errorf("convoy list should contain 'listed-convoy', got:\n%s", out)
	}
}

// --- gc convoy status ---

func TestConvoyStatus_ShowsDetails(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "status-test")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, createOut)
	}
	id := parseConvoyID(createOut)
	if id == "" {
		t.Fatal("could not parse convoy ID")
	}

	out, err := c.GC("convoy", "status", id)
	if err != nil {
		t.Fatalf("gc convoy status: %v\n%s", err, out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("status should contain convoy ID %q, got:\n%s", id, out)
	}
	if !strings.Contains(out, "status-test") {
		t.Errorf("status should contain convoy title, got:\n%s", out)
	}
}

func TestConvoyStatus_MissingID_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "status")
	if err == nil {
		t.Fatal("expected error for convoy status without ID, got success")
	}
	if !strings.Contains(out, "missing convoy ID") {
		t.Errorf("expected 'missing convoy ID' error, got:\n%s", out)
	}
}

func TestConvoyStatus_Nonexistent_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	_, err := c.GC("convoy", "status", "gc-99999")
	if err == nil {
		t.Fatal("expected error for nonexistent convoy, got success")
	}
}

// --- gc convoy target ---

func TestConvoyTarget_SetsBranch(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "target-test")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, createOut)
	}
	id := parseConvoyID(createOut)
	if id == "" {
		t.Fatal("could not parse convoy ID")
	}

	out, err := c.GC("convoy", "target", id, "main")
	if err != nil {
		t.Fatalf("gc convoy target: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Set target") {
		t.Errorf("expected 'Set target' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("expected 'main' in output, got:\n%s", out)
	}
}

func TestConvoyTarget_MissingArgs_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// cobra.ExactArgs(2) rejects this before our handler runs.
	_, err := c.GC("convoy", "target")
	if err == nil {
		t.Fatal("expected error for convoy target without args, got success")
	}
}

// --- gc convoy close ---

func TestConvoyClose_ClosesConvoy(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "close-test")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, createOut)
	}
	id := parseConvoyID(createOut)
	if id == "" {
		t.Fatal("could not parse convoy ID")
	}

	out, err := c.GC("convoy", "close", id)
	if err != nil {
		t.Fatalf("gc convoy close: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Closed convoy") {
		t.Errorf("expected 'Closed convoy' in output, got:\n%s", out)
	}

	// Closed convoy should not appear in list.
	listOut, err := c.GC("convoy", "list")
	if err != nil {
		t.Fatalf("gc convoy list: %v\n%s", err, listOut)
	}
	if strings.Contains(listOut, "close-test") {
		t.Error("closed convoy should not appear in list")
	}
}

func TestConvoyClose_MissingID_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "close")
	if err == nil {
		t.Fatal("expected error for convoy close without ID, got success")
	}
	if !strings.Contains(out, "missing convoy ID") {
		t.Errorf("expected 'missing convoy ID' error, got:\n%s", out)
	}
}

func TestConvoyClose_Nonexistent_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	_, err := c.GC("convoy", "close", "gc-99999")
	if err == nil {
		t.Fatal("expected error for closing nonexistent convoy, got success")
	}
}

// --- gc convoy check ---

func TestConvoyCheck_EmptyCity_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "check")
	if err != nil {
		t.Fatalf("gc convoy check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "auto-closed") {
		t.Errorf("expected 'auto-closed' summary in output, got:\n%s", out)
	}
}

// --- gc convoy stranded ---

func TestConvoyStranded_NoConvoys_ShowsNone(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "stranded")
	if err != nil {
		t.Fatalf("gc convoy stranded: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No stranded work") {
		t.Errorf("expected 'No stranded work' on fresh city, got:\n%s", out)
	}
}

// --- gc convoy land ---

func TestConvoyLand_OwnedConvoy_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "land-test", "--owned")
	if err != nil {
		t.Fatalf("gc convoy create --owned: %v\n%s", err, createOut)
	}
	id := parseConvoyID(createOut)
	if id == "" {
		t.Fatal("could not parse convoy ID")
	}

	out, err := c.GC("convoy", "land", id)
	if err != nil {
		t.Fatalf("gc convoy land: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Landed convoy") {
		t.Errorf("expected 'Landed convoy' in output, got:\n%s", out)
	}
}

func TestConvoyLand_NotOwned_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "not-owned")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, createOut)
	}
	id := parseConvoyID(createOut)
	if id == "" {
		t.Fatal("could not parse convoy ID")
	}

	out, err := c.GC("convoy", "land", id)
	if err == nil {
		t.Fatal("expected error landing non-owned convoy, got success")
	}
	if !strings.Contains(out, "not owned") {
		t.Errorf("expected 'not owned' error, got:\n%s", out)
	}
}

func TestConvoyLand_MissingID_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// cobra.ExactArgs(1) rejects this before our handler runs.
	_, err := c.GC("convoy", "land")
	if err == nil {
		t.Fatal("expected error for convoy land without ID, got success")
	}
}

func TestConvoyLand_DryRun_DoesNotClose(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	createOut, err := c.GC("convoy", "create", "dry-run-test", "--owned")
	if err != nil {
		t.Fatalf("gc convoy create: %v\n%s", err, createOut)
	}
	id := parseConvoyID(createOut)
	if id == "" {
		t.Fatal("could not parse convoy ID")
	}

	out, err := c.GC("convoy", "land", id, "--dry-run")
	if err != nil {
		t.Fatalf("gc convoy land --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Would land") {
		t.Errorf("expected 'Would land' in dry-run output, got:\n%s", out)
	}

	// Convoy should still appear in list (not actually closed).
	listOut, err := c.GC("convoy", "list")
	if err != nil {
		t.Fatalf("gc convoy list: %v\n%s", err, listOut)
	}
	if !strings.Contains(listOut, "dry-run-test") {
		t.Error("dry-run should not close the convoy, but it disappeared from list")
	}
}

// --- gc convoy add ---

func TestConvoyAdd_MissingArgs_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("convoy", "add")
	if err == nil {
		t.Fatal("expected error for convoy add without args, got success")
	}
	if !strings.Contains(out, "usage") {
		t.Errorf("expected usage message, got:\n%s", out)
	}
}

// --- helpers ---

// parseConvoyID extracts the convoy ID from "Created convoy gc-N ..." output.
func parseConvoyID(output string) string {
	// Format: "Created convoy gc-1 ..."
	prefix := "Created convoy "
	idx := strings.Index(output, prefix)
	if idx < 0 {
		return ""
	}
	rest := output[idx+len(prefix):]
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
