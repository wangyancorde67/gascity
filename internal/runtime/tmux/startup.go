package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

// startOps abstracts tmux operations needed by the startup sequence.
// This enables unit testing without a real tmux server.
type startOps interface {
	createSession(name, workDir, command string, env map[string]string) error
	isSessionRunning(name string) bool
	isRuntimeRunning(name string, processNames []string) bool
	killSession(name string) error
	waitForCommand(ctx context.Context, name string, timeout time.Duration) error
	acceptStartupDialogs(ctx context.Context, name string) error
	waitForReady(ctx context.Context, name string, rc *RuntimeConfig, timeout time.Duration) error
	hasSession(name string) (bool, error)
	sendKeys(name, text string) error
	setRemainOnExit(name string) error
	runSetupCommand(ctx context.Context, cmd string, env map[string]string, timeout time.Duration) error
}

// tmuxStartOps adapts [*Tmux] to the [startOps] interface.
type tmuxStartOps struct{ tm *Tmux }

func (o *tmuxStartOps) createSession(name, workDir, command string, env map[string]string) error {
	if command != "" || len(env) > 0 {
		return o.tm.NewSessionWithCommandAndEnv(name, workDir, command, env)
	}
	return o.tm.NewSession(name, workDir)
}

func (o *tmuxStartOps) isSessionRunning(name string) bool {
	return o.tm.IsSessionRunning(name)
}

func (o *tmuxStartOps) isRuntimeRunning(name string, processNames []string) bool {
	return o.tm.IsRuntimeRunning(name, processNames)
}

func (o *tmuxStartOps) killSession(name string) error {
	return o.tm.KillSessionWithProcesses(name)
}

func (o *tmuxStartOps) waitForCommand(ctx context.Context, name string, timeout time.Duration) error {
	return o.tm.WaitForCommand(ctx, name, supportedShells, timeout)
}

func (o *tmuxStartOps) acceptStartupDialogs(ctx context.Context, name string) error {
	return o.tm.AcceptStartupDialogs(ctx, name)
}

func (o *tmuxStartOps) waitForReady(ctx context.Context, name string, rc *RuntimeConfig, timeout time.Duration) error {
	return o.tm.WaitForRuntimeReady(ctx, name, rc, timeout)
}

func (o *tmuxStartOps) hasSession(name string) (bool, error) {
	return o.tm.HasSession(name)
}

func (o *tmuxStartOps) sendKeys(name, text string) error {
	return o.tm.NudgeSession(name, text)
}

func (o *tmuxStartOps) setRemainOnExit(name string) error {
	return o.tm.SetRemainOnExit(name, true)
}

func (o *tmuxStartOps) runSetupCommand(ctx context.Context, cmd string, env map[string]string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	if workDir := strings.TrimSpace(env["GC_DIR"]); workDir != "" {
		c.Dir = workDir
	}
	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	// Expose the tmux socket name so session_setup scripts can use
	// "tmux -L $GC_TMUX_SOCKET" to reach the correct server.
	if o.tm.cfg.SocketName != "" {
		c.Env = append(c.Env, "GC_TMUX_SOCKET="+o.tm.cfg.SocketName)
	}
	return c.Run()
}

// doStartSession is the pure startup orchestration logic.
// Testable via fakeStartOps without a real tmux server.
// The setupTimeout parameter controls the per-command timeout for
// session_setup, session_setup_script, and pre_start commands.
func doStartSession(ctx context.Context, ops startOps, name string, cfg runtime.Config, setupTimeout time.Duration, reporters ...*startupReporter) error {
	reporter := selectStartupReporter(reporters)
	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 0: Run pre-start commands (directory/worktree preparation).
	if err := runPreStart(ctx, ops, cfg, setupTimeout); err != nil {
		return fmt.Errorf("running pre_start: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 1: Ensure fresh session (zombie detection).
	if err := ensureFreshSession(ops, name, cfg); err != nil {
		return err
	}

	// Enable remain-on-exit for crash forensics. Non-fatal, but not silent.
	reporter.startupWarning("set_remain_on_exit", ops.setRemainOnExit(name))
	if err := ctx.Err(); err != nil {
		return err
	}

	if !startupHintsPresent(cfg) {
		// Fire-and-forget: caller may SendImmediate before the agent is
		// fully interactive. This is an accepted narrow race — it only
		// occurs when no readiness hints are configured, and the message
		// lands in tmux scrollback where the agent picks it up at its
		// next turn boundary.
		return nil
	}

	// Step 2: Wait for agent command to appear (not still in shell).
	if len(cfg.ProcessNames) > 0 {
		reporter.startupWarning("wait_for_command", ops.waitForCommand(ctx, name, 30*time.Second))
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Step 3: Accept startup dialogs (workspace trust + bypass permissions).
	// Always attempted when process names are set, since any Claude-like
	// agent may show a trust dialog regardless of EmitsPermissionWarning.
	if shouldAcceptStartupDialogs(cfg) {
		reporter.startupWarning("accept_startup_dialogs", ops.acceptStartupDialogs(ctx, name))
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Step 4: Wait for runtime readiness.
	if runtimeReadyHintsPresent(cfg) {
		rc := &RuntimeConfig{Tmux: &RuntimeTmuxConfig{
			ReadyPromptPrefix: cfg.ReadyPromptPrefix,
			ReadyDelayMs:      cfg.ReadyDelayMs,
			ProcessNames:      cfg.ProcessNames,
		}}
		reporter.startupWarning("wait_for_ready", ops.waitForReady(ctx, name, rc, 60*time.Second))
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Some CLIs surface trust or permissions dialogs only after their initial
	// ready screen. Re-run dialog acceptance after readiness so late dialogs do
	// not strand the session in an unusable startup state.
	if shouldAcceptStartupDialogs(cfg) {
		reporter.startupWarning("accept_startup_dialogs", ops.acceptStartupDialogs(ctx, name))
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Step 5: Verify session survived startup.
	alive, err := ops.hasSession(name)
	if err != nil {
		return fmt.Errorf("verifying session: %w", err)
	}
	if !alive {
		return fmt.Errorf("session %q died during startup", name)
	}

	// Step 5.5: Run session setup commands and script.
	if err := ctx.Err(); err != nil {
		return err
	}
	runSessionSetup(ctx, ops, name, cfg, setupTimeout, reporter)

	// Step 6: Send nudge text if configured.
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Nudge != "" {
		reporter.startupWarning("send_nudge", ops.sendKeys(name, cfg.Nudge))
	}

	// Step 6.5: Run session_live commands (idempotent, re-applicable).
	if err := ctx.Err(); err != nil {
		return err
	}
	runSessionLive(ctx, ops, name, cfg, setupTimeout, reporter)

	return nil
}

func startupHintsPresent(cfg runtime.Config) bool {
	return runtimeReadyHintsPresent(cfg) ||
		len(cfg.ProcessNames) > 0 ||
		cfg.EmitsPermissionWarning ||
		cfg.Nudge != "" ||
		len(cfg.PreStart) > 0 ||
		len(cfg.SessionSetup) > 0 ||
		cfg.SessionSetupScript != "" ||
		len(cfg.SessionLive) > 0
}

func runtimeReadyHintsPresent(cfg runtime.Config) bool {
	return cfg.ReadyPromptPrefix != "" || cfg.ReadyDelayMs > 0
}

func shouldAcceptStartupDialogs(cfg runtime.Config) bool {
	return len(cfg.ProcessNames) > 0 || cfg.EmitsPermissionWarning
}

// runSessionSetup runs session_setup commands then session_setup_script.
// Non-fatal: warnings on failure, session still works.
func runSessionSetup(ctx context.Context, ops startOps, name string, cfg runtime.Config, setupTimeout time.Duration, reporters ...*startupReporter) {
	if len(cfg.SessionSetup) == 0 && cfg.SessionSetupScript == "" {
		return
	}

	reporter := selectStartupReporter(reporters)
	setupEnv := buildSetupEnv(name, cfg.Env)

	for i, cmd := range cfg.SessionSetup {
		reporter.sessionSetupWarning(i, ops.runSetupCommand(ctx, cmd, setupEnv, setupTimeout))
	}

	if cfg.SessionSetupScript != "" {
		reporter.sessionSetupScriptWarning(ops.runSetupCommand(ctx, cfg.SessionSetupScript, setupEnv, setupTimeout))
	}
}

// runSessionLive runs session_live commands (idempotent, re-applicable).
// Called at startup after nudge, and by the reconciler on live-only drift.
// Non-fatal: warnings on failure, session still works.
func runSessionLive(ctx context.Context, ops startOps, name string, cfg runtime.Config, setupTimeout time.Duration, reporters ...*startupReporter) {
	if len(cfg.SessionLive) == 0 {
		return
	}

	reporter := selectStartupReporter(reporters)
	setupEnv := buildSetupEnv(name, cfg.Env)

	for i, cmd := range cfg.SessionLive {
		reporter.sessionLiveWarning(i, ops.runSetupCommand(ctx, cmd, setupEnv, setupTimeout))
	}
}

func buildSetupEnv(name string, env map[string]string) map[string]string {
	setupEnv := make(map[string]string, len(env)+1)
	for k, v := range env {
		setupEnv[k] = v
	}
	setupEnv["GC_SESSION"] = name
	return setupEnv
}

// runPreStart runs pre_start commands before session creation.
// Used for directory/worktree preparation. Failures are fatal because
// launching into an unprepared workDir can point agents at the wrong repo or
// skip required bootstrap state entirely.
func runPreStart(ctx context.Context, ops startOps, cfg runtime.Config, setupTimeout time.Duration) error {
	if len(cfg.PreStart) == 0 {
		return nil
	}

	setupEnv := make(map[string]string, len(cfg.Env))
	for k, v := range cfg.Env {
		setupEnv[k] = v
	}
	for i, cmd := range cfg.PreStart {
		if err := ops.runSetupCommand(ctx, cmd, setupEnv, setupTimeout); err != nil {
			return fmt.Errorf("pre_start[%d]: %w", i, err)
		}
	}
	return nil
}

// ensureFreshSession creates a session, handling stale tmux state.
// If the session already exists, returns an error (duplicate detection).
// Exceptions:
//   - dead panes (remain-on-exit corpses) are recycled even without ProcessNames
//   - if ProcessNames are configured and the agent is dead (zombie), the
//     zombie session is killed and recreated
//
// maxInlinePromptLen is the threshold above which prompts are written to a
// temp file and read back via $(cat ...) inside the tmux session. tmux
// new-session passes the command through a fixed-size protocol buffer
// (~2KB) so large prompts cause "command too long" errors.
const maxInlinePromptLen = 1024

func ensureFreshSession(ops startOps, name string, cfg runtime.Config) error {
	fullCommand := cfg.Command
	if cfg.PromptSuffix != "" {
		fullCommand = buildPromptedCommand(name, cfg)
	}

	err := ops.createSession(name, cfg.WorkDir, fullCommand, cfg.Env)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNoServer) {
		time.Sleep(50 * time.Millisecond)
		err = ops.createSession(name, cfg.WorkDir, fullCommand, cfg.Env)
		if err == nil {
			return nil
		}
	}
	if !errors.Is(err, ErrSessionExists) {
		return fmt.Errorf("creating session: %w", err)
	}

	if !ops.isSessionRunning(name) {
		if err := ops.killSession(name); err != nil {
			return fmt.Errorf("killing dead session: %w", err)
		}
		if err := recreateSessionAfterCleanup(ops, name, cfg.WorkDir, fullCommand, cfg.Env); err != nil {
			return fmt.Errorf("creating session after dead-session cleanup: %w", err)
		}
		return nil
	}

	if len(cfg.ProcessNames) == 0 {
		return fmt.Errorf("%w: session %q", runtime.ErrSessionExists, name)
	}

	if ops.isRuntimeRunning(name, cfg.ProcessNames) {
		return fmt.Errorf("%w: session %q", runtime.ErrSessionExists, name)
	}

	if err := ops.killSession(name); err != nil {
		return fmt.Errorf("killing zombie session: %w", err)
	}
	if err := recreateSessionAfterCleanup(ops, name, cfg.WorkDir, fullCommand, cfg.Env); err != nil {
		return fmt.Errorf("creating session after zombie cleanup: %w", err)
	}
	return nil
}

func buildPromptedCommand(name string, cfg runtime.Config) string {
	fullCommand := cfg.Command
	if len(cfg.PromptSuffix) > maxInlinePromptLen && cfg.WorkDir != "" {
		promptFile, err := writePromptFile(cfg.WorkDir, name, cfg.PromptSuffix)
		if err == nil {
			if cfg.PromptFlag != "" {
				return fmt.Sprintf(`sh -c 'exec %s %s "$(cat %q)" && rm -f %q'`,
					cfg.Command, cfg.PromptFlag, promptFile, promptFile)
			}
			return fmt.Sprintf(`sh -c 'exec %s "$(cat %q)" && rm -f %q'`,
				cfg.Command, promptFile, promptFile)
		}
	}

	if cfg.PromptFlag != "" {
		return fullCommand + " " + cfg.PromptFlag + " " + cfg.PromptSuffix
	}
	return fullCommand + " " + cfg.PromptSuffix
}

func recreateSessionAfterCleanup(ops startOps, name, workDir, command string, env map[string]string) error {
	err := ops.createSession(name, workDir, command, env)
	if errors.Is(err, ErrNoServer) {
		time.Sleep(50 * time.Millisecond)
		err = ops.createSession(name, workDir, command, env)
	}
	if errors.Is(err, ErrSessionExists) {
		return nil
	}
	return err
}

// writePromptFile writes a shell-quoted prompt string to a temp file in
// the agent's working directory. The file contains the raw prompt text
// (unquoted) so it can be read back via $(cat ...) inside the shell.
// Returns the file path on success.
func writePromptFile(workDir, agentName, shellQuotedPrompt string) (string, error) {
	raw := shellQuotedPrompt
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		raw = raw[1 : len(raw)-1]
		raw = strings.ReplaceAll(raw, `'\''`, `'`)
	}

	dir := filepath.Join(workDir, ".gc", "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	f, err := os.CreateTemp(dir, "prompt-"+agentName+"-*.txt")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(raw); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return "", errors.Join(err, closeErr)
		}
		if removeErr := os.Remove(f.Name()); removeErr != nil {
			return "", errors.Join(err, removeErr)
		}
		return "", err
	}
	if err := f.Close(); err != nil {
		if removeErr := os.Remove(f.Name()); removeErr != nil {
			return "", errors.Join(err, removeErr)
		}
		return "", err
	}
	return f.Name(), nil
}
