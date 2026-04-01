//go:build acceptance_a

// Config command acceptance tests.
//
// These exercise gc config show and gc config explain as a black box.
// Config commands inspect the resolved multi-layer city configuration
// including includes, packs, patches, and overrides.
package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// --- gc config (bare command) ---

func TestConfig_NoSubcommand_ShowsHelp(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config")
	if err != nil {
		t.Fatalf("gc config: %v\n%s", err, out)
	}
	if !strings.Contains(out, "show") || !strings.Contains(out, "explain") {
		t.Errorf("config help should mention subcommands 'show' and 'explain', got:\n%s", out)
	}
}

// --- gc config show ---

func TestConfigShow_TutorialCity_OutputsTOML(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config", "show")
	if err != nil {
		t.Fatalf("gc config show: %v\n%s", err, out)
	}
	// The output should be TOML with a [workspace] section.
	if !strings.Contains(out, "[workspace]") {
		t.Errorf("config show should contain [workspace] section, got:\n%s", out)
	}
}

func TestConfigShow_GastownCity_ContainsAgents(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	out, err := c.GC("config", "show")
	if err != nil {
		t.Fatalf("gc config show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[[agent]]") {
		t.Errorf("gastown config show should contain [[agent]] entries, got:\n%s", out)
	}
}

func TestConfigShow_Validate_ValidCity_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config", "show", "--validate")
	if err != nil {
		t.Fatalf("gc config show --validate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "valid") {
		t.Errorf("expected 'valid' in output, got:\n%s", out)
	}
}

func TestConfigShow_Validate_GastownCity_Succeeds(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	out, err := c.GC("config", "show", "--validate")
	if err != nil {
		t.Fatalf("gc config show --validate gastown: %v\n%s", err, out)
	}
	if !strings.Contains(out, "valid") {
		t.Errorf("expected 'valid' in gastown validation output, got:\n%s", out)
	}
}

func TestConfigShow_Provenance_ShowsSources(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config", "show", "--provenance")
	if err != nil {
		t.Fatalf("gc config show --provenance: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Sources") {
		t.Errorf("provenance output should contain 'Sources', got:\n%s", out)
	}
}

func TestConfigShow_Provenance_GastownShowsPacks(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	out, err := c.GC("config", "show", "--provenance")
	if err != nil {
		t.Fatalf("gc config show --provenance gastown: %v\n%s", err, out)
	}
	// Gastown packs contribute agents, so provenance should list multiple sources.
	if !strings.Contains(out, "Sources") {
		t.Errorf("provenance should show 'Sources', got:\n%s", out)
	}
	// Should show agent provenance from pack files.
	if !strings.Contains(out, "Agents") {
		t.Errorf("gastown provenance should show 'Agents' section, got:\n%s", out)
	}
}

func TestConfigShow_ExtraFile_LayersOverride(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Write an overlay file that adds an agent.
	overlay := filepath.Join(c.Dir, "overlay.toml")
	err := os.WriteFile(overlay, []byte(`
[[agent]]
name = "overlay-tester"
start_command = "echo hi"
`), 0o644)
	if err != nil {
		t.Fatalf("writing overlay: %v", err)
	}

	out, err := c.GC("config", "show", "-f", overlay)
	if err != nil {
		t.Fatalf("gc config show -f overlay: %v\n%s", err, out)
	}
	if !strings.Contains(out, "overlay-tester") {
		t.Errorf("config show with overlay should include overlay-tester agent, got:\n%s", out)
	}
}

func TestConfigShow_NotACity_ReturnsError(t *testing.T) {
	emptyDir := t.TempDir()
	_, err := helpers.RunGC(testEnv, emptyDir, "config", "show")
	if err == nil {
		t.Fatal("expected error for config show on non-city directory, got success")
	}
}

// --- gc config explain ---

func TestConfigExplain_TutorialCity_ShowsAgentDetails(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config", "explain")
	if err != nil {
		t.Fatalf("gc config explain: %v\n%s", err, out)
	}
	// Tutorial city has at least one agent; explain should show "Agent:" lines.
	if !strings.Contains(out, "Agent:") {
		t.Errorf("config explain should show 'Agent:' entries, got:\n%s", out)
	}
}

func TestConfigExplain_GastownCity_ShowsMultipleAgents(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	out, err := c.GC("config", "explain")
	if err != nil {
		t.Fatalf("gc config explain gastown: %v\n%s", err, out)
	}
	// Gastown has multiple agents — count "Agent:" lines.
	count := strings.Count(out, "Agent:")
	if count < 2 {
		t.Errorf("gastown should have multiple agents in explain output, found %d, output:\n%s", count, out)
	}
}

func TestConfigExplain_AgentFilter_ShowsOnlyThatAgent(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Find an agent name from the explain output (Agent: lines).
	explainAll, err := c.GC("config", "explain")
	if err != nil {
		t.Fatalf("gc config explain: %v\n%s", err, explainAll)
	}
	agentName := ""
	for _, line := range strings.Split(explainAll, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Agent:") {
			agentName = strings.TrimSpace(strings.TrimPrefix(line, "Agent:"))
			break
		}
	}
	if agentName == "" {
		t.Skip("no agent found in tutorial config to filter")
	}

	out, err := c.GC("config", "explain", "--agent", agentName)
	if err != nil {
		t.Fatalf("gc config explain --agent %s: %v\n%s", agentName, err, out)
	}
	if !strings.Contains(out, agentName) {
		t.Errorf("explain --agent %s should show that agent, got:\n%s", agentName, out)
	}
	// Should only show one Agent: line.
	if strings.Count(out, "Agent:") != 1 {
		t.Errorf("filtered explain should show exactly one agent, got:\n%s", out)
	}
}

func TestConfigExplain_AgentFilter_Nonexistent_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	_, err := c.GC("config", "explain", "--agent", "nonexistent-agent-xyz")
	if err == nil {
		t.Fatal("expected error for nonexistent agent filter, got success")
	}
}

func TestConfigExplain_ShowsProvenance(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config", "explain")
	if err != nil {
		t.Fatalf("gc config explain: %v\n%s", err, out)
	}
	// Each field line should have a provenance comment (# filename).
	if !strings.Contains(out, "#") {
		t.Errorf("explain output should contain provenance comments (#), got:\n%s", out)
	}
}

func TestConfigExplain_NotACity_ReturnsError(t *testing.T) {
	emptyDir := t.TempDir()
	_, err := helpers.RunGC(testEnv, emptyDir, "config", "explain")
	if err == nil {
		t.Fatal("expected error for config explain on non-city directory, got success")
	}
}

func TestConfigExplain_ExtraFile_IncludesOverlayAgent(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	overlay := filepath.Join(c.Dir, "overlay.toml")
	err := os.WriteFile(overlay, []byte(`
[[agent]]
name = "explain-overlay"
start_command = "echo test"
`), 0o644)
	if err != nil {
		t.Fatalf("writing overlay: %v", err)
	}

	out, err := c.GC("config", "explain", "-f", overlay, "--agent", "explain-overlay")
	if err != nil {
		t.Fatalf("gc config explain -f overlay --agent explain-overlay: %v\n%s", err, out)
	}
	if !strings.Contains(out, "explain-overlay") {
		t.Errorf("explain with overlay should show overlay agent, got:\n%s", out)
	}
}
