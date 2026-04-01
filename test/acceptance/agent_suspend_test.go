//go:build acceptance_a

// Agent and city suspend/resume acceptance tests.
//
// These exercise gc agent add/suspend/resume and gc suspend/resume
// (city-level) as a black box. All tests use subprocess session provider
// and file beads — no supervisor needed. Agent and city commands fall
// through to direct city.toml mutation when no API server is available.
package acceptance_test

import (
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// --- gc agent add ---

// TestAgentAdd_NewAgent_AppendsToConfig verifies that gc agent add
// appends an [[agent]] block to city.toml.
func TestAgentAdd_NewAgent_AppendsToConfig(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "add", "--name", "reviewer")
	if err != nil {
		t.Fatalf("gc agent add failed: %v\n%s", err, out)
	}

	toml := c.ReadFile("city.toml")
	if !strings.Contains(toml, "reviewer") {
		t.Errorf("city.toml should contain agent 'reviewer':\n%s", toml)
	}
}

// TestAgentAdd_WithPromptTemplate verifies that --prompt-template is
// recorded in the config.
func TestAgentAdd_WithPromptTemplate(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "add", "--name", "planner",
		"--prompt-template", "prompts/planner.md")
	if err != nil {
		t.Fatalf("gc agent add failed: %v\n%s", err, out)
	}

	toml := c.ReadFile("city.toml")
	if !strings.Contains(toml, "planner") {
		t.Errorf("city.toml missing agent name:\n%s", toml)
	}
	if !strings.Contains(toml, "prompts/planner.md") {
		t.Errorf("city.toml missing prompt_template:\n%s", toml)
	}
}

// TestAgentAdd_Duplicate_ReturnsError verifies that adding an agent
// with the same name fails.
func TestAgentAdd_Duplicate_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "add", "--name", "dupetest")
	if err != nil {
		t.Fatalf("first add failed: %v\n%s", err, out)
	}

	out, err = c.GC("agent", "add", "--name", "dupetest")
	if err == nil {
		t.Fatal("expected error for duplicate agent, got success")
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("expected 'already exists' error, got:\n%s", out)
	}
}

// TestAgentAdd_MissingName_ReturnsError verifies that gc agent add
// without --name returns an error.
func TestAgentAdd_MissingName_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "add")
	if err == nil {
		t.Fatal("expected error for missing --name, got success")
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("expected 'missing' in error, got:\n%s", out)
	}
}

// TestAgentAdd_Suspended verifies that --suspended registers the agent
// in a suspended state.
func TestAgentAdd_Suspended(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "add", "--name", "dormant", "--suspended")
	if err != nil {
		t.Fatalf("gc agent add --suspended failed: %v\n%s", err, out)
	}

	toml := c.ReadFile("city.toml")
	if !strings.Contains(toml, "suspended") {
		t.Errorf("city.toml should contain 'suspended' for the agent:\n%s", toml)
	}
}

// --- gc agent suspend / resume ---

// TestAgentSuspend_ThenResume verifies the full agent suspend/resume
// cycle using an agent that exists in the initial config. The agent
// must be in city.toml before init so the supervisor knows about it.
func TestAgentSuspend_ThenResume(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Stop the supervisor so agent suspend/resume falls through to
	// direct city.toml mutation (the API would reject an agent it
	// doesn't know about from config reload).
	c.GC("supervisor", "stop")

	// Write config with a known agent.
	c.WriteConfig(`[workspace]
name = "suspagent"

[[agent]]
name = "toggleagent"
start_command = "echo hello"
`)

	// Suspend.
	out, err := c.GC("agent", "suspend", "toggleagent")
	if err != nil {
		t.Fatalf("agent suspend: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Suspended") {
		t.Errorf("expected 'Suspended' in output, got:\n%s", out)
	}

	// Verify config has suspended=true.
	toml := c.ReadFile("city.toml")
	if !strings.Contains(toml, "suspended") {
		t.Errorf("city.toml should contain 'suspended' after suspend:\n%s", toml)
	}

	// Resume.
	out, err = c.GC("agent", "resume", "toggleagent")
	if err != nil {
		t.Fatalf("agent resume: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Resumed") {
		t.Errorf("expected 'Resumed' in output, got:\n%s", out)
	}
}

// TestAgentSuspend_MissingName_ReturnsError verifies that gc agent
// suspend without a name returns an error.
func TestAgentSuspend_MissingName_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "suspend")
	if err == nil {
		t.Fatal("expected error for missing name, got success")
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("expected 'missing' in error, got:\n%s", out)
	}
}

// TestAgentResume_MissingName_ReturnsError verifies that gc agent
// resume without a name returns an error.
func TestAgentResume_MissingName_ReturnsError(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("agent", "resume")
	if err == nil {
		t.Fatal("expected error for missing name, got success")
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("expected 'missing' in error, got:\n%s", out)
	}
}

// --- gc suspend / resume (city-level) ---

// TestCitySuspend_ThenResume verifies the city-level suspend/resume
// cycle: suspend the city, verify gc hook returns empty, resume,
// verify gc hook works again.
func TestCitySuspend_ThenResume(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Write a config with an agent so hook has something to look for.
	c.WriteConfig(`[workspace]
name = "susptest"

[[agent]]
name = "worker"
work_query = "echo no-work"
`)

	// Suspend the city.
	out, err := c.GC("suspend")
	if err != nil {
		t.Fatalf("gc suspend failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "suspended") {
		t.Errorf("expected 'suspended' in output, got:\n%s", out)
	}

	// Hook should return error (city suspended).
	out, err = c.GC("hook", "worker")
	if err == nil {
		t.Error("expected gc hook to fail while city is suspended")
	}

	// Resume the city.
	out, err = c.GC("resume")
	if err != nil {
		t.Fatalf("gc resume failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "resumed") {
		t.Errorf("expected 'resumed' in output, got:\n%s", out)
	}

	// Hook should work again (returns 1 for no work, but not an error about suspension).
	out, _ = c.GC("hook", "worker")
	if strings.Contains(out, "suspended") {
		t.Errorf("hook should not mention 'suspended' after resume:\n%s", out)
	}
}

// TestCitySuspend_NotACity_ReturnsError verifies that gc suspend on
// a non-city directory returns an error.
func TestCitySuspend_NotACity_ReturnsError(t *testing.T) {
	emptyDir := t.TempDir()
	out, err := helpers.RunGC(testEnv, emptyDir, "suspend")
	if err == nil {
		t.Fatal("expected error suspending non-city directory")
	}
	_ = out
}
