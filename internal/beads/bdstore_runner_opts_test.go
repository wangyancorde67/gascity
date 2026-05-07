//go:build !windows

package beads

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunner_RespectsPerCallTimeout verifies that NewExecCommandRunner
// honors a per-runner timeout supplied via WithSubprocessTimeout, rather
// than the package-level default. Critical for the tick-critical bd
// write path, which must use a tighter budget (30s) than the
// big-list-read default (120s).
//
// Architecture: ga-f4m2.1.
func TestRunner_RespectsPerCallTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "sleep.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := NewExecCommandRunner(WithSubprocessTimeout(2 * time.Second))
	start := time.Now()
	_, err := runner(dir, script)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("runner unexpectedly succeeded")
	}
	if elapsed < 2*time.Second || elapsed > 5*time.Second {
		t.Fatalf("elapsed = %s, want roughly 2-5s for a 2s timeout", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %q does not wrap context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("error %q missing 'timed out after' marker", err)
	}
}

// TestNewExecCommandRunner_DefaultTimeoutMatchesGlobal ensures that when
// no options are supplied, the runner falls back to the package-level
// default (preserved as bdCommandTimeout = 120s today). This locks in
// backwards-compatibility for ExecCommandRunner() and
// ExecCommandRunnerWithEnv() callers that pre-date the options API.
//
// Architecture: ga-f4m2.1.
func TestNewExecCommandRunner_DefaultTimeoutMatchesGlobal(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}

	oldTimeout := bdCommandTimeout
	bdCommandTimeout = 75 * time.Millisecond
	t.Cleanup(func() { bdCommandTimeout = oldTimeout })

	dir := t.TempDir()
	script := filepath.Join(dir, "sleep.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := NewExecCommandRunner()
	start := time.Now()
	_, err := runner(dir, script)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("runner unexpectedly succeeded")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %s, want fast timeout when default is 75ms", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %q does not wrap context.DeadlineExceeded", err)
	}
}
