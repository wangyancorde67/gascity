// Package tmux provides a wrapper for tmux session operations via subprocess.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

// Provenance: This file was copied from github.com/steveyegge/gastown
// internal/tmux/tmux.go at upstream/main a4387800b619 (2026-02-22).
// External dependencies on gastown's config, constants, and telemetry
// packages were inlined. See issue/PR references in comments for history.

// ---------------------------------------------------------------------------
// Inlined constants from gastown/internal/constants.
// These preserve the exact values from the original to avoid subtle behavioral
// regressions (timing, debounce, shell detection).
// ---------------------------------------------------------------------------

const pollInterval = 100 * time.Millisecond

// Config holds configurable timeouts and intervals for the tmux provider.
// All fields have sensible defaults matching the original hardcoded values.
type Config struct {
	SetupTimeout       time.Duration
	NudgeReadyTimeout  time.Duration
	NudgeRetryInterval time.Duration
	NudgeLockTimeout   time.Duration
	// NudgeIdleTimeout is how long Nudge waits for the agent to become idle
	// before sending the message. This prevents interrupting active tool calls.
	// If the agent doesn't become idle within this timeout, the message is
	// sent anyway (immediate fallback). Set to 0 to disable wait-idle and
	// always send immediately.
	NudgeIdleTimeout time.Duration
	DebounceMs       int
	DisplayMs        int
	// SocketName specifies the tmux socket name for per-city isolation.
	// When set, all tmux commands use "tmux -L <socket>" to connect to
	// a dedicated server. Empty means use the default tmux server.
	SocketName string
}

// DefaultConfig returns a Config with the original hardcoded values.
func DefaultConfig() Config {
	return Config{
		SetupTimeout:       10 * time.Second,
		NudgeReadyTimeout:  10 * time.Second,
		NudgeRetryInterval: 500 * time.Millisecond,
		NudgeLockTimeout:   30 * time.Second,
		NudgeIdleTimeout:   30 * time.Second,
		DebounceMs:         500,
		DisplayMs:          5000,
	}
}

// supportedShells lists shell binaries that can be detected in tmux panes.
var supportedShells = []string{"bash", "zsh", "sh", "fish", "tcsh", "ksh"}

// Role emoji mapping (used only by SetStatusFormat for status bar display).
var roleEmoji = map[string]string{
	"mayor":        "🎩",
	"deacon":       "🐺",
	"witness":      "🦉",
	"refinery":     "🏭",
	"crew":         "👷",
	"polecat":      "😺",
	"coordinator":  "🎩",
	"health-check": "🐺",
}

// ---------------------------------------------------------------------------
// Minimal types inlined from gastown/internal/config.
// Only the fields actually used by tmux operations are included.
// ---------------------------------------------------------------------------

// RuntimeConfig holds LLM runtime configuration relevant to tmux operations.
// This is a minimal subset of gastown's config.RuntimeConfig — only the fields
// that WaitForRuntimeReady actually reads.
type RuntimeConfig struct {
	Tmux *RuntimeTmuxConfig
}

// RuntimeTmuxConfig controls tmux heuristics for detecting runtime readiness.
type RuntimeTmuxConfig struct {
	ProcessNames      []string // tmux pane commands indicating runtime is running
	ReadyPromptPrefix string   // prompt prefix to detect readiness (e.g., "> ")
	ReadyDelayMs      int      // fixed delay used when prompt detection unavailable
}

// sessionNudgeLocks serializes nudges to the same session.
// This prevents interleaving when multiple nudges arrive concurrently,
// which can cause garbled input and missed Enter keys.
// Uses channel-based semaphores instead of sync.Mutex to support
// timed lock acquisition — preventing permanent lockout if a nudge hangs.
var sessionNudgeLocks sync.Map // map[string]chan struct{}

// validSessionNameRe validates session names to prevent shell injection
var validSessionNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Common errors
var (
	ErrNoServer           = errors.New("no tmux server running")
	ErrSessionExists      = errors.New("session already exists")
	ErrEnvironmentNotSet  = errors.New("tmux environment variable not set")
	ErrSessionNotFound    = errors.New("session not found")
	ErrInvalidSessionName = errors.New("invalid session name")
	ErrIdleTimeout        = errors.New("agent not idle before timeout")
)

// validateSessionName checks that a session name contains only safe characters.
// Returns ErrInvalidSessionName if the name contains dots, colons, or other
// characters that cause tmux to silently fail or produce cryptic errors.
func validateSessionName(name string) error {
	if name == "" || !validSessionNameRe.MatchString(name) {
		return fmt.Errorf("%w %q: must match %s", ErrInvalidSessionName, name, validSessionNameRe.String())
	}
	return nil
}

// executor runs tmux subprocess commands.
// Abstracted for unit testing of argument construction (socket flags, etc.).
type executor interface {
	execute(args []string) (string, error)
	executeCtx(ctx context.Context, args []string) (string, error)
}

// realExecutor runs actual tmux subprocesses.
type realExecutor struct{}

func (realExecutor) execute(args []string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", wrapError(err, stderr.String(), args)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (realExecutor) executeCtx(ctx context.Context, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", wrapError(err, stderr.String(), args)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Tmux wraps tmux operations.
type Tmux struct {
	cfg                  Config
	exec                 executor
	interactionDedup     *approvalDedup
	interactionDedupOnce sync.Once
	hiddenAttachMu       sync.Mutex
	hiddenAttachClients  map[string]*hiddenAttachClient
}

// NewTmux creates a new Tmux wrapper with default configuration.
func NewTmux() *Tmux {
	return &Tmux{cfg: DefaultConfig(), exec: realExecutor{}}
}

// NewTmuxWithConfig creates a new Tmux wrapper with the given configuration.
func NewTmuxWithConfig(cfg Config) *Tmux {
	return &Tmux{cfg: cfg, exec: realExecutor{}}
}

func (t *Tmux) approvalDedup() *approvalDedup {
	t.interactionDedupOnce.Do(func() {
		t.interactionDedup = &approvalDedup{lastHash: make(map[string]string)}
	})
	return t.interactionDedup
}

// runCtx executes a tmux command with a context (for timeout/cancellation).
func (t *Tmux) runCtx(ctx context.Context, args ...string) (string, error) {
	allArgs := []string{"-u"}
	if t.cfg.SocketName != "" {
		allArgs = append(allArgs, "-L", t.cfg.SocketName)
	}
	allArgs = append(allArgs, args...)
	return t.exec.executeCtx(ctx, allArgs)
}

// run executes a tmux command and returns stdout.
// All commands include -u flag for UTF-8 support regardless of locale settings.
// When SocketName is configured, -L <socket> is injected after -u.
// See: https://github.com/steveyegge/gastown/issues/1219
func (t *Tmux) run(args ...string) (string, error) {
	allArgs := []string{"-u"}
	if t.cfg.SocketName != "" {
		allArgs = append(allArgs, "-L", t.cfg.SocketName)
	}
	allArgs = append(allArgs, args...)

	return t.exec.execute(allArgs)
}

// wrapError wraps tmux errors with context.
func wrapError(err error, stderr string, args []string) error {
	stderr = strings.TrimSpace(stderr)

	// Detect specific error types
	if strings.Contains(stderr, "no server running") ||
		strings.Contains(stderr, "error connecting to") ||
		strings.Contains(stderr, "no current target") ||
		strings.Contains(stderr, "server exited unexpectedly") {
		return ErrNoServer
	}
	if strings.Contains(stderr, "duplicate session") {
		return ErrSessionExists
	}
	if strings.Contains(stderr, "session not found") ||
		strings.Contains(stderr, "can't find session") ||
		strings.Contains(stderr, "can't find pane") {
		return ErrSessionNotFound
	}

	if stderr != "" {
		return fmt.Errorf("tmux %s: %s", args[0], stderr)
	}
	return fmt.Errorf("tmux %s: %w", args[0], err)
}

// NewSession creates a new detached tmux session.
func (t *Tmux) NewSession(name, workDir string) error {
	if err := validateSessionName(name); err != nil {
		return err
	}
	args := []string{"new-session", "-d", "-s", name}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	_, err := t.run(args...)
	if err != nil {
		return err
	}
	// tmux 3.3+ sets window-size=manual on detached sessions, locking them
	// at 80x24 even after a client attaches. Reset to "latest" so the window
	// adapts to the largest attached client.
	t.run("set-option", "-wt", name, "window-size", "latest") //nolint:errcheck // best-effort
	return nil
}

// NewSessionWithCommand creates a new detached tmux session that immediately runs a command.
// Unlike NewSession + SendKeys, this avoids race conditions where the shell isn't ready
// or the command arrives before the shell prompt. The command runs directly as the
// initial process of the pane.
// See: https://github.com/anthropics/gastown/issues/280
func (t *Tmux) NewSessionWithCommand(name, workDir, command string) error {
	if err := validateSessionName(name); err != nil {
		return err
	}
	args := []string{"new-session", "-d", "-s", name}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	// Add the command as the last argument - tmux runs it as the pane's initial process
	args = append(args, command)
	_, err := t.run(args...)
	if err != nil {
		return err
	}
	// tmux 3.3+: reset window-size from manual to latest (see NewSession).
	t.run("set-option", "-wt", name, "window-size", "latest") //nolint:errcheck // best-effort
	return nil
}

// NewSessionWithCommandAndEnv creates a new detached tmux session with environment
// variables set via -e flags. This ensures the initial shell process inherits the
// correct environment from the session, rather than inheriting from the tmux server
// or parent process. The -e flags set session-level environment before the shell
// starts, preventing stale env vars (e.g., GT_ROLE from a parent mayor session)
// from leaking into crew/polecat shells.
//
// The command should still use 'exec env' for WaitForCommand detection compatibility,
// but -e provides defense-in-depth for the initial shell environment.
// Requires tmux >= 3.2.
func (t *Tmux) NewSessionWithCommandAndEnv(name, workDir, command string, env map[string]string) error {
	if err := validateSessionName(name); err != nil {
		return err
	}
	// Disable mouse mode and monitor-activity before creating the session.
	// With mouse on, tmux sends SGR mouse tracking sequences (\x1b[<...M)
	// into panes. When the gc controller polls tmux state (list-panes,
	// capture-pane, display-message), these sequences can arrive as stray
	// ESC bytes on the agent's stdin. Claude Code's TUI misinterprets lone
	// ESC as an Escape keypress, triggering "Interrupted" mid-tool-call.
	// Automated agents don't need mouse input, so disabling is safe.
	defer func() {
		t.run("set-option", "-t", name, "mouse", "off")             //nolint:errcheck
		t.run("set-option", "-wt", name, "monitor-activity", "off") //nolint:errcheck
	}()
	args := []string{"new-session", "-d", "-s", name}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	// Add -e flags to set environment variables in the session before the shell starts.
	// Keys are sorted for deterministic behavior.
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var unsetKeys []string
	for _, k := range keys {
		if env[k] == "" {
			// Empty values mean "unset this var". Collect for env -u prefix.
			unsetKeys = append(unsetKeys, k)
		} else {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, env[k]))
		}
	}
	// For vars that need unsetting, prefix the command with env -u flags.
	// tmux -e sets session-level env but the shell process still inherits
	// from the tmux server's global environment. env -u ensures the var
	// is actually absent from the child process.
	if len(unsetKeys) > 0 && command != "" {
		var prefix string
		for _, k := range unsetKeys {
			prefix += " -u " + k
		}
		command = "env" + prefix + " " + command
	}
	// Add the command as the last argument
	args = append(args, command)
	_, err := t.run(args...)
	if err != nil {
		return err
	}
	// tmux 3.3+: reset window-size from manual to latest (see NewSession).
	t.run("set-option", "-wt", name, "window-size", "latest") //nolint:errcheck // best-effort
	return nil
}

// EnsureSessionFresh ensures a session is available and healthy.
// If the session exists but is a zombie (Claude not running), it kills the session first.
// This prevents "session already exists" errors when trying to restart dead agents.
//
// A session is considered a zombie if:
// - The tmux session exists
// - But Claude (node process) is not running in it
//
// Uses create-first approach to avoid TOCTOU race conditions in multi-agent
// environments where another agent could create the same session between a
// check and create call.
//
// Returns nil if session was created successfully or already exists with a running agent.
func (t *Tmux) EnsureSessionFresh(name, workDir string) error {
	if err := validateSessionName(name); err != nil {
		return err
	}

	// Try to create the session first (atomic — avoids check-then-create race)
	err := t.NewSession(name, workDir)
	if err == nil {
		return nil // Created successfully
	}
	if !errors.Is(err, ErrSessionExists) {
		return fmt.Errorf("creating session: %w", err)
	}

	// Session already exists — check if it's a zombie
	if t.IsAgentRunning(name) {
		// Session is healthy (agent running) — nothing to do
		return nil
	}

	// Zombie session: tmux alive but agent dead
	// Kill it so we can create a fresh one
	// Use KillSessionWithProcesses to ensure all descendant processes are killed
	if err := t.KillSessionWithProcesses(name); err != nil {
		return fmt.Errorf("killing zombie session: %w", err)
	}

	// Create fresh session (handle race: another agent may have created it
	// between our kill and this create — that's fine, treat as success)
	err = t.NewSession(name, workDir)
	if errors.Is(err, ErrSessionExists) {
		return nil
	}
	return err
}

// KillSession terminates a tmux session.
func (t *Tmux) KillSession(name string) error {
	_, err := t.run("kill-session", "-t", name)
	return err
}

// KillServer terminates the entire tmux server and all sessions.
func (t *Tmux) KillServer() error {
	_, err := t.run("kill-server")
	if errors.Is(err, ErrNoServer) {
		return nil // Already dead
	}
	return err
}

// SetExitEmpty controls the tmux exit-empty server option.
// When on (default), the server exits when there are no sessions.
// When off, the server stays running even with no sessions.
// This is useful during shutdown to prevent the server from exiting
// when all Gas Town sessions are killed but the user has no other sessions.
func (t *Tmux) SetExitEmpty(on bool) error {
	value := "on"
	if !on {
		value = "off"
	}
	_, err := t.run("set-option", "-g", "exit-empty", value)
	if errors.Is(err, ErrNoServer) {
		return nil // No server to configure
	}
	return err
}

// IsAvailable checks if tmux is installed and can be invoked.
func (t *Tmux) IsAvailable() bool {
	cmd := exec.Command("tmux", "-V")
	return cmd.Run() == nil
}

// HasSession checks if a session exists (exact match).
// Uses "=" prefix for exact matching, preventing prefix matches
// (e.g., "gt-deacon-boot" won't match when checking for "gt-deacon").
func (t *Tmux) HasSession(name string) (bool, error) {
	_, err := t.run("has-session", "-t", "="+name)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListSessions returns all session names.
func (t *Tmux) ListSessions() ([]string, error) {
	out, err := t.run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return nil, nil // No server = no sessions
		}
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	return strings.Split(out, "\n"), nil
}

// SessionSet provides O(1) session existence checks by caching session names.
// Use this when you need to check multiple sessions to avoid N+1 subprocess calls.
type SessionSet struct {
	sessions map[string]struct{}
}

// NewSessionSet creates a SessionSet from a list of session names.
// This is useful for testing or when session names are known from another source.
func NewSessionSet(names []string) *SessionSet {
	set := &SessionSet{
		sessions: make(map[string]struct{}, len(names)),
	}
	for _, name := range names {
		set.sessions[name] = struct{}{}
	}
	return set
}

// GetSessionSet returns a SessionSet containing all current sessions.
// Call this once at the start of an operation, then use Has() for O(1) checks.
// This replaces multiple HasSession() calls with a single ListSessions() call.
//
// Builds the map directly from tmux output to avoid intermediate slice allocation.
func (t *Tmux) GetSessionSet() (*SessionSet, error) {
	out, err := t.run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return &SessionSet{sessions: make(map[string]struct{})}, nil
		}
		return nil, err
	}

	// Count newlines to pre-size map (avoids rehashing during insertion)
	count := strings.Count(out, "\n") + 1
	set := &SessionSet{
		sessions: make(map[string]struct{}, count),
	}

	// Parse directly without intermediate slice allocation
	for len(out) > 0 {
		idx := strings.IndexByte(out, '\n')
		var line string
		if idx >= 0 {
			line = out[:idx]
			out = out[idx+1:]
		} else {
			line = out
			out = ""
		}
		if line != "" {
			set.sessions[line] = struct{}{}
		}
	}
	return set, nil
}

// Has returns true if the session exists in the set.
// This is an O(1) lookup - no subprocess is spawned.
func (s *SessionSet) Has(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.sessions[name]
	return ok
}

// Names returns all session names in the set.
func (s *SessionSet) Names() []string {
	if s == nil || len(s.sessions) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		names = append(names, name)
	}
	return names
}

// ListSessionIDs returns a map of session name to session ID.
// Session IDs are in the format "$N" where N is a number.
func (t *Tmux) ListSessionIDs() (map[string]string, error) {
	out, err := t.run("list-sessions", "-F", "#{session_name}:#{session_id}")
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return nil, nil // No server = no sessions
		}
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	result := make(map[string]string)
	skipped := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Parse "name:$id" format
		idx := strings.Index(line, ":")
		if idx > 0 && idx < len(line)-1 {
			name := line[:idx]
			id := line[idx+1:]
			result[name] = id
		} else {
			skipped++
		}
	}
	// Note: skipped lines are silently ignored for backward compatibility
	_ = skipped
	return result, nil
}

// SendKeys sends keystrokes to a session and presses Enter.
// Always sends Enter as a separate command for reliability.
// Uses a debounce delay between paste and Enter to ensure paste completes.
func (t *Tmux) SendKeys(session, keys string) error {
	return t.SendKeysDebounced(session, keys, t.cfg.DebounceMs)
}

// SendKeysDebounced sends keystrokes with a configurable delay before Enter.
// The debounceMs parameter controls how long to wait after paste before sending Enter.
// This prevents race conditions where Enter arrives before paste is processed.
func (t *Tmux) SendKeysDebounced(session, keys string, debounceMs int) error {
	// Send text using literal mode (-l) to handle special chars
	if _, err := t.run("send-keys", "-t", session, "-l", keys); err != nil {
		return err
	}
	// Wait for paste to be processed
	if debounceMs > 0 {
		time.Sleep(time.Duration(debounceMs) * time.Millisecond)
	}
	// Send Enter separately - more reliable than appending to send-keys
	_, err := t.run("send-keys", "-t", session, "Enter")
	return err
}

// SendKeysRaw sends keystrokes without adding Enter.
func (t *Tmux) SendKeysRaw(session, keys string) error {
	_, err := t.run("send-keys", "-t", session, keys)
	return err
}

// SendKeysReplace sends keystrokes, clearing any pending input first.
// This is useful for "replaceable" notifications where only the latest matters.
// Uses Ctrl-U to clear the input line before sending the new message.
// The delay parameter controls how long to wait after clearing before sending (ms).
func (t *Tmux) SendKeysReplace(session, keys string, clearDelayMs int) error {
	// Send Ctrl-U to clear any pending input on the line
	if _, err := t.run("send-keys", "-t", session, "C-u"); err != nil {
		return err
	}

	// Small delay to let the clear take effect
	if clearDelayMs > 0 {
		time.Sleep(time.Duration(clearDelayMs) * time.Millisecond)
	}

	// Now send the actual message
	return t.SendKeys(session, keys)
}

// SendKeysDelayed sends keystrokes after a delay (in milliseconds).
// Useful for waiting for a process to be ready before sending input.
func (t *Tmux) SendKeysDelayed(session, keys string, delayMs int) error {
	time.Sleep(time.Duration(delayMs) * time.Millisecond)
	return t.SendKeys(session, keys)
}

// SendKeysDelayedDebounced sends keystrokes after a pre-delay, with a custom debounce before Enter.
// Use this when sending input to a process that needs time to initialize AND the message
// needs extra time between paste and Enter (e.g., Claude prompt injection).
// preDelayMs: time to wait before sending text (for process readiness)
// debounceMs: time to wait between text paste and Enter key (for paste completion)
func (t *Tmux) SendKeysDelayedDebounced(session, keys string, preDelayMs, debounceMs int) error {
	if preDelayMs > 0 {
		time.Sleep(time.Duration(preDelayMs) * time.Millisecond)
	}
	return t.SendKeysDebounced(session, keys, debounceMs)
}

// getSessionNudgeSem returns the channel semaphore for serializing nudges to a session.
// Creates a new semaphore if one doesn't exist for this session.
// The semaphore is a buffered channel of size 1 — send to acquire, receive to release.
func getSessionNudgeSem(session string) chan struct{} {
	sem := make(chan struct{}, 1)
	actual, _ := sessionNudgeLocks.LoadOrStore(session, sem)
	return actual.(chan struct{})
}

// acquireNudgeLock attempts to acquire the per-session nudge lock with a timeout.
// Returns true if the lock was acquired, false if the timeout expired.
func acquireNudgeLock(session string, timeout time.Duration) bool {
	sem := getSessionNudgeSem(session)
	select {
	case sem <- struct{}{}:
		return true
	case <-time.After(timeout):
		return false
	}
}

// releaseNudgeLock releases the per-session nudge lock.
func releaseNudgeLock(session string) {
	sem := getSessionNudgeSem(session)
	select {
	case <-sem:
	default:
		// Lock wasn't held — shouldn't happen, but don't block
	}
}

// AcceptStartupDialogs dismisses all Claude Code startup dialogs that can block
// automated sessions. Delegates to the shared [runtime.AcceptStartupDialogs]
// with tmux-specific peek and send-keys callbacks.
//
// Call this after starting Claude and waiting for it to initialize (WaitForCommand),
// but before sending any prompts. Idempotent: safe to call on sessions without dialogs.
func (t *Tmux) AcceptStartupDialogs(ctx context.Context, sess string) error {
	return t.DismissKnownDialogs(ctx, sess, 8*time.Second)
}

// DismissKnownDialogs dismisses known trust, permissions, and rate-limit
// dialogs using a bounded timeout.
func (t *Tmux) DismissKnownDialogs(ctx context.Context, sess string, timeout time.Duration) error {
	return runtime.AcceptStartupDialogsWithTimeout(ctx, timeout,
		func(lines int) (string, error) { return t.CapturePane(sess, lines) },
		func(keys ...string) error {
			for _, k := range keys {
				if _, err := t.run("send-keys", "-t", sess, k); err != nil {
					return err
				}
			}
			return nil
		},
	)
}

// GetPaneCommand returns the current command running in a pane.
// Returns "bash", "zsh", "claude", "node", etc.
func (t *Tmux) GetPaneCommand(session string) (string, error) {
	// Use :^.0 (first window, first pane) to target the agent pane
	// regardless of tmux's base-index setting. The literal :0.0 fails
	// when base-index is 1 (a common tmux.conf setting), causing tmux
	// to resolve against the active window instead.
	out, err := t.run("display-message", "-t", session+":^.0", "-p", "#{pane_current_command}")
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(out)
	if result == "" {
		return "", fmt.Errorf("empty command for session %s (session may not exist)", session)
	}
	return result, nil
}

// FindAgentPane finds the pane running an agent process within a session.
// In multi-pane sessions, send-keys -t <session> targets the active/focused pane,
// which may not be the agent pane. This method enumerates all panes and returns
// the pane ID (e.g., "%5") of the one running the agent.
//
// Detection checks pane_current_command, then falls back to process tree inspection
// (same logic as IsRuntimeRunning) to handle agents started via shell wrappers.
//
// Returns ("", nil) if the session has only one pane (no disambiguation needed),
// or if no agent pane can be identified (caller should fall back to session targeting).
func (t *Tmux) FindAgentPane(session string) (string, error) {
	// List all panes across all windows (-s) with ID, command, and PID.
	// Without -s, list-panes only shows the active window's panes, missing
	// agent panes in other windows.
	out, err := t.run("list-panes", "-s", "-t", session, "-F", "#{pane_id}\t#{pane_current_command}\t#{pane_pid}")
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) <= 1 {
		// Single pane - no disambiguation needed
		return "", nil
	}

	// Get agent process names from session environment
	processNames := t.resolveSessionProcessNames(session)

	// Check each pane for agent process
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		paneID := parts[0]
		paneCmd := parts[1]
		panePID := parts[2]

		// Direct command match
		for _, name := range processNames {
			if paneCmd == name {
				return paneID, nil
			}
		}

		// Shell with agent descendant
		for _, shell := range supportedShells {
			if paneCmd == shell && hasDescendantWithNames(panePID, processNames, 0) {
				return paneID, nil
			}
		}

		// Version-as-argv[0] (e.g., "2.1.30") — check real binary name
		if processMatchesNames(panePID, processNames) {
			return paneID, nil
		}
	}

	// No agent pane found
	return "", nil
}

// GetPaneID returns the pane identifier for a session's first pane.
// Returns a pane ID like "%0" that can be used with RespawnPane.
// Targets first window (:^.0) to be consistent with GetPaneCommand,
// GetPanePID, and GetPaneWorkDir.
func (t *Tmux) GetPaneID(session string) (string, error) {
	out, err := t.run("display-message", "-t", session+":^.0", "-p", "#{pane_id}")
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(out)
	if result == "" {
		return "", fmt.Errorf("no panes found in session %s", session)
	}
	return result, nil
}

// GetPaneWorkDir returns the current working directory of a pane.
// Targets first window (:^.0) to avoid returning the active pane's
// working directory in multi-pane sessions.
func (t *Tmux) GetPaneWorkDir(session string) (string, error) {
	out, err := t.run("display-message", "-t", session+":^.0", "-p", "#{pane_current_path}")
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(out)
	if result == "" {
		return "", fmt.Errorf("empty working directory for session %s (session may not exist)", session)
	}
	return result, nil
}

// GetPanePID returns the PID of the pane's main process.
// When target is a session name, explicitly targets pane 0 (:0.0) to avoid
// returning the active pane's PID in multi-pane sessions. When target is
// a pane ID (e.g., "%5"), uses it directly.
func (t *Tmux) GetPanePID(target string) (string, error) {
	tmuxTarget := target
	if !strings.HasPrefix(target, "%") {
		tmuxTarget = target + ":^.0"
	}
	out, err := t.run("display-message", "-t", tmuxTarget, "-p", "#{pane_pid}")
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(out)
	if result == "" {
		return "", fmt.Errorf("empty PID for target %s (session may not exist)", target)
	}
	return result, nil
}

// IsPaneDead reports whether the target pane's process has exited while the
// pane remains visible (for example because remain-on-exit is enabled).
// When target is a session name, pane 0 is queried explicitly.
func (t *Tmux) IsPaneDead(target string) (bool, error) {
	tmuxTarget := target
	if !strings.HasPrefix(target, "%") {
		tmuxTarget = target + ":^.0"
	}
	out, err := t.run("display-message", "-t", tmuxTarget, "-p", "#{pane_dead}")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("unexpected pane_dead value %q for target %s", out, target)
	}
}

// IsSessionRunning reports whether the tmux session exists and its primary pane
// still has a live process. Dead panes kept by remain-on-exit are treated as
// not running.
func (t *Tmux) IsSessionRunning(session string) bool {
	has, err := t.HasSession(session)
	if err != nil || !has {
		return false
	}
	dead, err := t.IsPaneDead(session)
	if err != nil {
		// Fall back to session existence on query failures to avoid false
		// negatives when tmux cannot report pane state.
		return true
	}
	return !dead
}

// GetSessionActivity returns the last meaningful activity time for a session.
//
// For detached agent sessions, tmux's #{session_activity} does not advance on
// pane I/O — it effectively sticks to creation/attach time. Query per-window
// activity instead and take the most recent timestamp so detached output and
// send-keys both count as activity.
func (t *Tmux) GetSessionActivity(session string) (time.Time, error) {
	out, err := t.run("list-windows", "-t", session, "-F", "#{window_activity}")
	if err != nil {
		return time.Time{}, err
	}

	timestamp, err := latestActivityTimestamp(out)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(timestamp, 0), nil
}

func latestActivityTimestamp(out string) (int64, error) {
	var latest int64
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		timestamp, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsing window activity %q: %w", line, err)
		}
		if timestamp > latest {
			latest = timestamp
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("parsing window activity: no timestamps found")
	}
	return latest, nil
}

// ZombieStatus describes the liveness state of a tmux agent session.
type ZombieStatus int

const (
	// SessionHealthy means the session exists and the agent process is alive.
	SessionHealthy ZombieStatus = iota
	// SessionDead means the tmux session does not exist.
	SessionDead
	// AgentDead means the tmux session exists but the agent process has died.
	AgentDead
	// AgentHung means the tmux session and agent process exist but there has
	// been no tmux activity for longer than the specified threshold.
	AgentHung
)

// String returns a human-readable label for the zombie status.
func (z ZombieStatus) String() string {
	switch z {
	case SessionHealthy:
		return "healthy"
	case SessionDead:
		return "session-dead"
	case AgentDead:
		return "agent-dead"
	case AgentHung:
		return "agent-hung"
	default:
		return "unknown"
	}
}

// IsZombie returns true if the status represents a zombie (any non-healthy state
// where the session exists but the agent is dead or hung).
func (z ZombieStatus) IsZombie() bool {
	return z == AgentDead || z == AgentHung
}

// CheckSessionHealth determines the health status of an agent session.
// It performs three levels of checking:
//  1. Session existence (tmux has-session)
//  2. Agent process liveness (IsAgentAlive — checks process tree)
//  3. Activity staleness (GetSessionActivity — checks tmux output timestamp)
//
// The maxInactivity parameter controls how long a session can be idle before
// being considered hung. Pass 0 to skip activity checking (only check process
// liveness). A reasonable default for production is 10-15 minutes.
//
// This is the preferred unified method for zombie detection across all agent types.
func (t *Tmux) CheckSessionHealth(session string, maxInactivity time.Duration) ZombieStatus {
	// Level 1: Does the tmux session exist?
	alive, err := t.HasSession(session)
	if err != nil || !alive {
		return SessionDead
	}

	// Level 2: Is the agent process running inside the session?
	if !t.IsAgentAlive(session) {
		return AgentDead
	}

	// Level 3: Has there been recent activity? (optional)
	if maxInactivity > 0 {
		lastActivity, err := t.GetSessionActivity(session)
		if err == nil && !lastActivity.IsZero() {
			if time.Since(lastActivity) > maxInactivity {
				return AgentHung
			}
		}
		// On error or zero time, skip activity check — don't false-positive
	}

	return SessionHealthy
}

// processMatchesNames checks if a process's binary name matches any of the given names.
// Uses ps to get the actual command name from the process's executable path.
// This handles cases where argv[0] is modified (e.g., Claude showing version "2.1.30").
func processMatchesNames(pid string, names []string) bool {
	if len(names) == 0 {
		return false
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[name] = struct{}{}
	}

	// Use ps to get the command name (COMM column gives the executable name)
	cmd := exec.Command("ps", "-p", pid, "-o", "comm=")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Get just the base name (in case it's a full path like /Users/.../claude)
	commPath := strings.TrimSpace(string(out))
	comm := filepath.Base(commPath)
	if _, ok := nameSet[comm]; ok {
		return true
	}

	// Fall back to argv[0] from the full command line. This catches wrapper
	// scripts launched as "/path/to/codex" where COMM may report "bash" or
	// another interpreter instead of the provider name.
	cmd = exec.Command("ps", "-p", pid, "-o", "args=")
	out, err = cmd.Output()
	if err != nil {
		return false
	}
	args := strings.Fields(strings.TrimSpace(string(out)))
	if len(args) == 0 {
		return false
	}
	argv0 := filepath.Base(args[0])
	if _, ok := nameSet[argv0]; ok {
		return true
	}

	// Wrapper runtimes often execute providers through interpreters such as bun,
	// node, or npx, leaving the actual provider name only in the first positional
	// argument. Only check the first non-flag argument after a known interpreter
	// to avoid false positives (e.g., "vim claude.txt" or "tail -f gemini.log").
	knownInterpreters := map[string]struct{}{
		"node": {}, "bun": {}, "npx": {}, "deno": {},
	}
	// Runner subcommands (e.g., "bun run gemini") that should be skipped
	// when scanning for the provider name in positional args.
	runnerSubcommands := map[string]struct{}{
		"run": {}, "exec": {}, "x": {},
	}
	if _, isInterpreter := knownInterpreters[argv0]; isInterpreter {
		for _, token := range args[1:] {
			token = strings.TrimSpace(token)
			if token == "" || strings.HasPrefix(token, "-") {
				continue
			}
			// Skip known runner subcommands like "run" in "bun run gemini".
			if _, isRunner := runnerSubcommands[token]; isRunner {
				continue
			}
			base := filepath.Base(token)
			if _, ok := nameSet[base]; ok {
				return true
			}
			baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
			if _, ok := nameSet[baseNoExt]; ok {
				return true
			}
			break // only check the first positional argument
		}
	}
	return false
}

// hasDescendantWithNames checks if a process has any descendant (child, grandchild, etc.)
// matching any of the given names. Recursively traverses the process tree up to maxDepth.
// Used when the pane command is a shell (bash, zsh) that launched an agent.
func hasDescendantWithNames(pid string, names []string, depth int) bool {
	const maxDepth = 10 // Prevent infinite loops in case of circular references
	if len(names) == 0 || depth > maxDepth {
		return false
	}
	// Use pgrep to find child processes.
	cmd := exec.Command("pgrep", "-P", pid)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		childPid := strings.TrimSpace(line)
		if childPid == "" {
			continue
		}
		if processMatchesNames(childPid, names) {
			return true
		}
		if hasDescendantWithNames(childPid, names, depth+1) {
			return true
		}
	}
	return false
}

// FindSessionByWorkDir finds tmux sessions where the pane's current working directory
// matches or is under the target directory. Returns session names that match.
// If processNames is provided, only returns sessions that match those processes.
// If processNames is nil or empty, returns all sessions matching the directory.
func (t *Tmux) FindSessionByWorkDir(targetDir string, processNames []string) ([]string, error) {
	sessions, err := t.ListSessions()
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, session := range sessions {
		if session == "" {
			continue
		}

		workDir, err := t.GetPaneWorkDir(session)
		if err != nil {
			continue // Skip sessions we can't query
		}

		// Check if workdir matches target (exact match or subdir)
		if workDir == targetDir || strings.HasPrefix(workDir, targetDir+"/") {
			if len(processNames) > 0 {
				if t.IsRuntimeRunning(session, processNames) {
					matches = append(matches, session)
				}
				continue
			}
			matches = append(matches, session)
		}
	}

	return matches, nil
}

// CapturePane captures the visible content of a pane.
func (t *Tmux) CapturePane(session string, lines int) (string, error) {
	content, err := t.run("capture-pane", "-p", "-t", session, "-S", fmt.Sprintf("-%d", lines))
	return content, err
}

// CapturePaneAll captures all scrollback history.
func (t *Tmux) CapturePaneAll(session string) (string, error) {
	return t.run("capture-pane", "-p", "-t", session, "-S", "-")
}

// CapturePaneLines captures the last N lines of a pane as a slice.
func (t *Tmux) CapturePaneLines(session string, lines int) ([]string, error) {
	out, err := t.CapturePane(session, lines)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// AttachSession attaches to an existing session.
// Note: This replaces the current process with tmux attach.
func (t *Tmux) AttachSession(session string) error {
	_, err := t.run("attach-session", "-t", session)
	return err
}

// SelectWindow selects a window by index.
func (t *Tmux) SelectWindow(session string, index int) error {
	_, err := t.run("select-window", "-t", fmt.Sprintf("%s:%d", session, index))
	return err
}

// SetEnvironment sets an environment variable in the session.
func (t *Tmux) SetEnvironment(session, key, value string) error {
	_, err := t.run("set-environment", "-t", session, key, value)
	return err
}

// RemoveEnvironment removes an environment variable from the session.
func (t *Tmux) RemoveEnvironment(session, key string) error {
	_, err := t.run("set-environment", "-t", session, "-u", key)
	return err
}

// GetEnvironment gets an environment variable from the session.
func (t *Tmux) GetEnvironment(session, key string) (string, error) {
	out, err := t.run("show-environment", "-t", session, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "-"+key {
		return "", fmt.Errorf("%w: %s", ErrEnvironmentNotSet, key)
	}
	// Output format: KEY=value
	parts := strings.SplitN(out, "=", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected environment format for %s: %q", key, out)
	}
	return parts[1], nil
}

// GetAllEnvironment returns all environment variables for a session.
func (t *Tmux) GetAllEnvironment(session string) (map[string]string, error) {
	out, err := t.run("show-environment", "-t", session)
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			// Skip empty lines and unset markers (lines starting with -)
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env, nil
}

// RenameSession renames a session.
func (t *Tmux) RenameSession(oldName, newName string) error {
	if err := validateSessionName(newName); err != nil {
		return err
	}
	_, err := t.run("rename-session", "-t", oldName, newName)
	return err
}

// SessionInfo contains information about a tmux session.
type SessionInfo struct {
	Name         string
	Windows      int
	Created      string
	Attached     bool
	Activity     string // Last activity time
	LastAttached string // Last time the session was attached
}

// DisplayMessage shows a message in the tmux status line.
// This is non-disruptive - it doesn't interrupt the session's input.
// Duration is specified in milliseconds.
func (t *Tmux) DisplayMessage(session, message string, durationMs int) error {
	// Set display time temporarily, show message, then restore
	// Use -d flag for duration in tmux 2.9+
	_, err := t.run("display-message", "-t", session, "-d", fmt.Sprintf("%d", durationMs), message)
	return err
}

// DisplayMessageDefault shows a message with default duration (5 seconds).
func (t *Tmux) DisplayMessageDefault(session, message string) error {
	return t.DisplayMessage(session, message, t.cfg.DisplayMs)
}

// SendNotificationBanner sends a visible notification banner to a tmux session.
// This interrupts the terminal to ensure the notification is seen.
// Uses echo to print a boxed banner with the notification details.
func (t *Tmux) SendNotificationBanner(session, from, subject string) error {
	// Sanitize inputs for shell safety — proper shell single-quote escaping.
	for _, p := range []*string{&from, &subject} {
		*p = strings.ReplaceAll(*p, "\n", " ")
		*p = strings.ReplaceAll(*p, "\r", " ")
		*p = strings.ReplaceAll(*p, "'", `'\''`)
	}

	// Build the banner text
	banner := fmt.Sprintf(`echo '
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📬 NEW MAIL from %s
Subject: %s
Run: gc mail inbox
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
'`, from, subject)

	return t.SendKeys(session, banner)
}

// IsAgentRunning checks if an agent appears to be running in the session.
//
// If expectedPaneCommands is non-empty, the pane's current command must match one of them.
// If expectedPaneCommands is empty, any non-shell command counts as "agent running".
func (t *Tmux) IsAgentRunning(session string, expectedPaneCommands ...string) bool {
	cmd, err := t.GetPaneCommand(session)
	if err != nil {
		return false
	}

	if len(expectedPaneCommands) > 0 {
		for _, expected := range expectedPaneCommands {
			if expected != "" && cmd == expected {
				return true
			}
		}
		return false
	}

	// Fallback: any non-shell command counts as running.
	for _, shell := range supportedShells {
		if cmd == shell {
			return false
		}
	}
	return cmd != ""
}

// IsRuntimeRunning checks if a runtime appears to be running in the session.
// Checks both pane command and child processes (for agents started via shell).
// This is the unified agent detection method for all agent types.
func (t *Tmux) IsRuntimeRunning(session string, processNames []string) bool {
	if len(processNames) == 0 {
		return false
	}
	cmd, err := t.GetPaneCommand(session)
	if err != nil {
		return false
	}
	// Check direct pane command match
	for _, name := range processNames {
		if cmd == name {
			return true
		}
	}
	// Check for child processes if pane command is a shell or unrecognized.
	// This handles:
	// - Agents started with "bash -c 'export ... && agent ...'"
	// - Claude Code showing version as argv[0] (e.g., "2.1.29")
	pid, err := t.GetPanePID(session)
	if err != nil || pid == "" {
		return false
	}
	// If pane command is a shell, check descendants
	for _, shell := range supportedShells {
		if cmd == shell {
			return hasDescendantWithNames(pid, processNames, 0)
		}
	}
	// If pane command is unrecognized (not in processNames, not a shell),
	// check if the process ITSELF matches (handles version-as-argv[0] like "2.1.30")
	// before checking descendants.
	if processMatchesNames(pid, processNames) {
		return true
	}
	// Finally check descendants as fallback
	return hasDescendantWithNames(pid, processNames, 0)
}

// IsAgentAlive checks if an agent is running in the session using agent-agnostic detection.
// It reads GT_PROCESS_NAMES from the session environment for accurate process detection,
// falling back to GT_AGENT-based lookup for legacy sessions.
// This is the preferred method for zombie detection across all agent types.
func (t *Tmux) IsAgentAlive(session string) bool {
	return t.IsRuntimeRunning(session, t.resolveSessionProcessNames(session))
}

// resolveSessionProcessNames returns the process names to check for a session.
// Prefers GT_PROCESS_NAMES (set at startup, handles custom agents that shadow
// built-in presets). Falls back to GT_AGENT-based lookup for legacy sessions.
func (t *Tmux) resolveSessionProcessNames(session string) []string {
	// Prefer explicit process names set at startup (handles custom agents correctly)
	if names, err := t.GetEnvironment(session, "GT_PROCESS_NAMES"); err == nil && names != "" {
		return strings.Split(names, ",")
	}
	// Fallback: default to Claude's process names for backwards compatibility.
	// In gastown this called config.GetProcessNames which resolved from preset
	// registry. Inlined here to avoid the config dependency.
	return []string{"node", "claude"}
}

// WaitForCommand polls until the pane is NOT running one of the excluded commands.
// Useful for waiting until a shell has started a new process (e.g., claude).
// Returns nil when a non-excluded command is detected, or error on timeout.
//
// GetSessionInfo returns detailed information about a session.
func (t *Tmux) GetSessionInfo(name string) (*SessionInfo, error) {
	format := "#{session_name}|#{session_windows}|#{session_created}|#{session_attached}|#{session_activity}|#{session_last_attached}"
	out, err := t.run("list-sessions", "-F", format, "-f", fmt.Sprintf("#{==:#{session_name},%s}", name))
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, ErrSessionNotFound
	}

	parts := strings.Split(out, "|")
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected session info format: %s", out)
	}

	windows := 0
	_, _ = fmt.Sscanf(parts[1], "%d", &windows) // non-fatal: defaults to 0 on parse error

	// Convert unix timestamp to formatted string for consumers.
	created := parts[2]
	var createdUnix int64
	if _, err := fmt.Sscanf(created, "%d", &createdUnix); err == nil && createdUnix > 0 {
		created = time.Unix(createdUnix, 0).Format("2006-01-02 15:04:05")
	}

	info := &SessionInfo{
		Name:     parts[0],
		Windows:  windows,
		Created:  created,
		Attached: parts[3] == "1",
	}

	// Activity and last attached are optional (may not be present in older tmux)
	if len(parts) > 4 {
		info.Activity = parts[4]
	}
	if len(parts) > 5 {
		info.LastAttached = parts[5]
	}

	return info, nil
}

// ApplyTheme sets the status bar style for a session.
func (t *Tmux) ApplyTheme(session string, theme Theme) error {
	_, err := t.run("set-option", "-t", session, "status-style", theme.Style())
	return err
}

// roleIcons maps role names to display icons for the status bar.
// Uses centralized emojis from constants package.
// Includes legacy keys ("coordinator", "health-check") for backwards compatibility.
var roleIcons = roleEmoji

// SetStatusFormat configures the left side of the status bar.
// Shows compact identity: icon + minimal context
func (t *Tmux) SetStatusFormat(session, rig, worker, role string) error {
	// Get icon for role (empty string if not found)
	icon := roleIcons[role]

	// Compact format - icon already identifies role
	// Mayor: 🎩 Mayor
	// Crew:  👷 gastown/crew/max (full path)
	// Polecat: 😺 gastown/Toast
	var left string
	if rig == "" {
		// Town-level agent (Mayor, Deacon) - keep as-is
		left = fmt.Sprintf("%s %s ", icon, worker)
	} else {
		// Rig agents - use session name (already in prefix format: gt-crew-gus)
		left = fmt.Sprintf("%s %s ", icon, session)
	}

	if _, err := t.run("set-option", "-t", session, "status-left-length", "25"); err != nil {
		return err
	}
	_, err := t.run("set-option", "-t", session, "status-left", left)
	return err
}

// SetDynamicStatus configures the right side with dynamic content.
// Uses a shell command that tmux calls periodically to get current status.
func (t *Tmux) SetDynamicStatus(session string) error {
	if err := validateSessionName(session); err != nil {
		return err
	}

	// tmux calls this command every status-interval seconds
	// gt status-line reads env vars and mail to build the status
	right := fmt.Sprintf(`#(gt status-line --session=%s 2>/dev/null) %%H:%%M`, session)

	if _, err := t.run("set-option", "-t", session, "status-right-length", "80"); err != nil {
		return err
	}
	// Set faster refresh for more responsive status
	if _, err := t.run("set-option", "-t", session, "status-interval", "5"); err != nil {
		return err
	}
	_, err := t.run("set-option", "-t", session, "status-right", right)
	return err
}

// ConfigureGasTownSession applies full Gas Town theming to a session.
// This is a convenience method that applies theme, status format, and dynamic status.
func (t *Tmux) ConfigureGasTownSession(session string, theme Theme, rig, worker, role string) error {
	if err := t.ApplyTheme(session, theme); err != nil {
		return fmt.Errorf("applying theme: %w", err)
	}
	if err := t.SetStatusFormat(session, rig, worker, role); err != nil {
		return fmt.Errorf("setting status format: %w", err)
	}
	if err := t.SetDynamicStatus(session); err != nil {
		return fmt.Errorf("setting dynamic status: %w", err)
	}
	if err := t.SetMailClickBinding(session); err != nil {
		return fmt.Errorf("setting mail click binding: %w", err)
	}
	if err := t.SetFeedBinding(session); err != nil {
		return fmt.Errorf("setting feed binding: %w", err)
	}
	if err := t.SetAgentsBinding(session); err != nil {
		return fmt.Errorf("setting agents binding: %w", err)
	}
	if err := t.SetCycleBindings(session); err != nil {
		return fmt.Errorf("setting cycle bindings: %w", err)
	}
	if err := t.EnableMouseMode(session); err != nil {
		return fmt.Errorf("enabling mouse mode: %w", err)
	}
	return nil
}

// EnableMouseMode enables mouse support and clipboard integration for a tmux session.
// This allows clicking to select panes/windows, scrolling with mouse wheel,
// and dragging to resize panes. Hold Shift for native terminal text selection.
// Also enables clipboard integration so copied text goes to system clipboard.
func (t *Tmux) EnableMouseMode(session string) error {
	if _, err := t.run("set-option", "-t", session, "mouse", "on"); err != nil {
		return err
	}
	// Enable clipboard integration with terminal (OSC 52)
	// This allows copying text to system clipboard when selecting with mouse
	_, err := t.run("set-option", "-t", session, "set-clipboard", "on")
	return err
}

// IsInsideTmux checks if the current process is running inside a tmux session.
// This is detected by the presence of the TMUX environment variable.
func IsInsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

// SetMailClickBinding configures left-click on status-right to show mail preview.
// This creates a popup showing the first unread message when clicking the mail icon area.
//
// The binding is conditional: it only activates in Gas Town sessions (those matching
// a registered rig prefix or "hq-"). In non-GT sessions, the user's original
// MouseDown1StatusRight binding (if any) is preserved.
// See: https://github.com/steveyegge/gastown/issues/1548
func (t *Tmux) SetMailClickBinding(_ string) error {
	// Skip if already configured — preserves user's original fallback from first call
	if t.isGTBinding("root", "MouseDown1StatusRight") {
		return nil
	}
	ifShell := fmt.Sprintf("echo '#{session_name}' | grep -Eq '%s'", sessionPrefixPattern())
	fallback := t.getKeyBinding("root", "MouseDown1StatusRight")
	if fallback == "" {
		// No prior binding — do nothing in non-GT sessions
		fallback = ":"
	}
	_, err := t.run("bind-key", "-T", "root", "MouseDown1StatusRight",
		"if-shell", ifShell,
		"display-popup -E -w 60 -h 15 'gt mail peek || echo No unread mail'",
		fallback)
	return err
}

// RespawnPane kills all processes in a pane and starts a new command.
// This is used for "hot reload" of agent sessions - instantly restart in place.
// The pane parameter should be a pane ID (e.g., "%0") or session:window.pane format.
func (t *Tmux) RespawnPane(pane, command string) error {
	_, err := t.run("respawn-pane", "-k", "-t", pane, command)
	return err
}

// RespawnPaneWithWorkDir kills all processes in a pane and starts a new command
// in the specified working directory. Use this when the pane's current working
// directory may have been deleted.
func (t *Tmux) RespawnPaneWithWorkDir(pane, workDir, command string) error {
	args := []string{"respawn-pane", "-k", "-t", pane}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	args = append(args, command)
	_, err := t.run(args...)
	return err
}

// ClearHistory clears the scrollback history buffer for a pane.
// This resets copy-mode display from [0/N] to [0/0].
// The pane parameter should be a pane ID (e.g., "%0") or session:window.pane format.
func (t *Tmux) ClearHistory(pane string) error {
	_, err := t.run("clear-history", "-t", pane)
	return err
}

// SetRemainOnExit controls whether a pane stays around after its process exits.
// When on, the pane remains with "[Exited]" status, allowing respawn-pane to restart it.
// When off (default), the pane is destroyed when its process exits.
// This is essential for handoff: set on before killing processes, so respawn-pane works.
func (t *Tmux) SetRemainOnExit(pane string, on bool) error {
	value := "on"
	if !on {
		value = "off"
	}
	_, err := t.run("set-option", "-t", pane, "remain-on-exit", value)
	return err
}

// SwitchClient switches the current tmux client to a different session.
// Used after remote recycle to move the user's view to the recycled session.
func (t *Tmux) SwitchClient(targetSession string) error {
	_, err := t.run("switch-client", "-t", targetSession)
	return err
}

// SetCrewCycleBindings sets up C-b n/p to cycle through sessions.
// This is now an alias for SetCycleBindings - the unified command detects
// session type automatically.
//
// IMPORTANT: We pass #{session_name} to the command because run-shell doesn't
// reliably preserve the session context. tmux expands #{session_name} at binding
// resolution time (when the key is pressed), giving us the correct session.
func (t *Tmux) SetCrewCycleBindings(session string) error {
	return t.SetCycleBindings(session)
}

// SetTownCycleBindings sets up C-b n/p to cycle through sessions.
// This is now an alias for SetCycleBindings - the unified command detects
// session type automatically.
func (t *Tmux) SetTownCycleBindings(session string) error {
	return t.SetCycleBindings(session)
}

// isGTBinding checks if the given key already has a Gas Town if-shell binding.
// Used to skip redundant re-binding on repeated ConfigureGasTownSession calls,
// preserving the user's original fallback captured on the first call.
func (t *Tmux) isGTBinding(table, key string) bool {
	output, err := t.run("list-keys", "-T", table, key)
	if err != nil || output == "" {
		return false
	}
	// GT bindings use if-shell with a run-shell/display-popup invoking "gt ".
	// Require both "if-shell" and "gt " to avoid false positives on user
	// bindings that happen to contain "gt " without the if-shell guard.
	return strings.Contains(output, "if-shell") && strings.Contains(output, "gt ")
}

// getKeyBinding returns the current tmux command bound to the given key in the
// specified key table. Returns empty string if no binding exists or if querying
// fails. This is used to capture user bindings before overwriting them, so the
// original binding can be preserved in the else branch of an if-shell guard.
//
// The returned string is a tmux command (e.g., "next-window", "run-shell 'lazygit'")
// suitable for use as a command argument to bind-key or if-shell.
//
// If the existing binding is already a Gas Town if-shell binding (detected by
// the presence of both "if-shell" and "gt " in the output), it is treated as
// no prior binding to avoid recursive wrapping on repeated calls.
func (t *Tmux) getKeyBinding(table, key string) string {
	// tmux list-keys -T <table> <key> outputs a line like:
	//   bind-key -T prefix g if-shell "..." "run-shell 'gt agents menu'" ":"
	// We need to extract just the command portion.
	//
	// Assumed format (tested with tmux 3.3+):
	//   bind-key [-r] -T <table> <key> <command...>
	// If tmux changes this format, parsing fails safely (returns ""),
	// which causes the caller to use its default fallback.
	output, err := t.run("list-keys", "-T", table, key)
	if err != nil || output == "" {
		return ""
	}

	// If this is already a Gas Town binding (from a previous ConfigureGasTownSession call),
	// don't capture it — we'd end up wrapping our own if-shell in another if-shell.
	// We check for both "if-shell" and "gt " to avoid false-positiving on user
	// bindings that happen to contain the substring "gt ".
	if strings.Contains(output, "if-shell") && strings.Contains(output, "gt ") {
		return ""
	}

	// Parse the binding command from list-keys output.
	// Format: "bind-key [-r] -T <table> <key> <command...>"
	// We need everything after the key name.
	// Find the key in the output and take everything after it.
	fields := strings.Fields(output)
	keyIdx := -1
	for i, f := range fields {
		if f == "-T" && i+2 < len(fields) {
			// Skip table name, the next field is the key
			keyIdx = i + 2
			break
		}
	}
	if keyIdx < 0 || keyIdx >= len(fields)-1 {
		return ""
	}

	// Everything after the key is the command
	// Rejoin from keyIdx+1 onward, but we need to preserve the original spacing.
	// Find the key token in the original string and take everything after it.
	idx := strings.Index(output, " "+fields[keyIdx]+" ")
	if idx < 0 {
		return ""
	}
	cmd := strings.TrimSpace(output[idx+len(" "+fields[keyIdx]+" "):])
	if cmd == "" {
		return ""
	}

	return cmd
}

// safePrefixRe matches the character set guaranteed by beadsPrefixRegexp in
// internal/rig/manager.go.  Used as defense-in-depth: if rigs.json is
// hand-edited with regex metacharacters or shell-special chars, we skip the
// entry rather than injecting it into a grep -Eq / tmux if-shell fragment.
var safePrefixRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,19}$`)

// PrefixResolver is a function that returns all registered session prefixes.
// In gastown this was config.AllRigPrefixes(townRoot). Callers inject their
// own resolver to avoid coupling tmux to the config package.
var PrefixResolver func() []string

// sessionPrefixPattern returns a grep -Eq pattern that matches any registered
// session name. The pattern is built dynamically from PrefixResolver (if set)
// so that rigs beyond the defaults are recognized.
//
// Example output: "^(bd|db|fa|gl|gt|hq|la|lc)-"
func sessionPrefixPattern() string {
	seen := map[string]bool{"hq": true, "gc": true} // always include defaults
	if PrefixResolver != nil {
		for _, p := range PrefixResolver() {
			if safePrefixRe.MatchString(p) {
				seen[p] = true
			}
		}
	}
	sorted := make([]string, 0, len(seen))
	for p := range seen {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	return "^(" + strings.Join(sorted, "|") + ")-"
}

// SetCycleBindings sets up C-b n/p to cycle through related sessions.
// The gt cycle command automatically detects the session type and cycles
// within the appropriate group:
// - Town sessions: Mayor ↔ Deacon
// - Crew sessions: All crew members in the same rig
//
// IMPORTANT: These bindings are conditional - they only run gt cycle for
// Gas Town sessions (those matching a registered rig prefix or "hq-").
// For non-GT sessions, the user's original binding is preserved. If no
// prior binding existed, the tmux defaults (next-window/previous-window)
// are used.
// See: https://github.com/steveyegge/gastown/issues/13
// See: https://github.com/steveyegge/gastown/issues/1548
//
// IMPORTANT: We pass #{session_name} to the command because run-shell doesn't
// reliably preserve the session context. tmux expands #{session_name} at binding
// resolution time (when the key is pressed), giving us the correct session.
func (t *Tmux) SetCycleBindings(_ string) error {
	// Skip if already configured — preserves user's original fallback from first call
	if t.isGTBinding("prefix", "n") {
		return nil
	}
	pattern := sessionPrefixPattern()
	ifShell := fmt.Sprintf("echo '#{session_name}' | grep -Eq '%s'", pattern)

	// Capture existing bindings before overwriting, falling back to tmux defaults
	nextFallback := t.getKeyBinding("prefix", "n")
	if nextFallback == "" {
		nextFallback = "next-window"
	}
	prevFallback := t.getKeyBinding("prefix", "p")
	if prevFallback == "" {
		prevFallback = "previous-window"
	}

	// C-b n → gt cycle next for Gas Town sessions, original binding otherwise
	if _, err := t.run("bind-key", "-T", "prefix", "n",
		"if-shell", ifShell,
		"run-shell 'gt cycle next --session #{session_name}'",
		nextFallback); err != nil {
		return err
	}
	// C-b p → gt cycle prev for Gas Town sessions, original binding otherwise
	if _, err := t.run("bind-key", "-T", "prefix", "p",
		"if-shell", ifShell,
		"run-shell 'gt cycle prev --session #{session_name}'",
		prevFallback); err != nil {
		return err
	}
	return nil
}

// SetFeedBinding configures C-b a to jump to the activity feed window.
// This creates the feed window if it doesn't exist, or switches to it if it does.
// Uses `gt feed --window` which handles both creation and switching.
//
// IMPORTANT: This binding is conditional - it only runs for Gas Town sessions
// (those matching a registered rig prefix or "hq-"). For non-GT sessions, the
// user's original binding is preserved. If no prior binding existed, the key
// press is silently ignored.
// See: https://github.com/steveyegge/gastown/issues/13
// See: https://github.com/steveyegge/gastown/issues/1548
func (t *Tmux) SetFeedBinding(_ string) error {
	// Skip if already configured — preserves user's original fallback from first call
	if t.isGTBinding("prefix", "a") {
		return nil
	}
	ifShell := fmt.Sprintf("echo '#{session_name}' | grep -Eq '%s'", sessionPrefixPattern())
	fallback := t.getKeyBinding("prefix", "a")
	if fallback == "" {
		// No prior binding — do nothing in non-GT sessions
		fallback = ":"
	}
	_, err := t.run("bind-key", "-T", "prefix", "a",
		"if-shell", ifShell,
		"run-shell 'gt feed --window'",
		fallback)
	return err
}

// SetAgentsBinding configures C-b g to open the agent switcher popup menu.
// This runs `gt agents menu` which displays a tmux popup with all Gas Town agents.
//
// IMPORTANT: This binding is conditional - it only runs for Gas Town sessions
// (those matching a registered rig prefix or "hq-"). For non-GT sessions, the
// user's original binding is preserved. If no prior binding existed, the key
// press is silently ignored.
// See: https://github.com/steveyegge/gastown/issues/1548
func (t *Tmux) SetAgentsBinding(_ string) error {
	// Skip if already configured — preserves user's original fallback from first call
	if t.isGTBinding("prefix", "g") {
		return nil
	}
	ifShell := fmt.Sprintf("echo '#{session_name}' | grep -Eq '%s'", sessionPrefixPattern())
	fallback := t.getKeyBinding("prefix", "g")
	if fallback == "" {
		// No prior binding — do nothing in non-GT sessions
		fallback = ":"
	}
	_, err := t.run("bind-key", "-T", "prefix", "g",
		"if-shell", ifShell,
		"run-shell 'gt agents menu'",
		fallback)
	return err
}

// GetSessionCreatedUnix returns the Unix timestamp when a session was created.
// Returns 0 if the session doesn't exist or can't be queried.
func (t *Tmux) GetSessionCreatedUnix(session string) (int64, error) {
	out, err := t.run("display-message", "-t", session, "-p", "#{session_created}")
	if err != nil {
		return 0, err
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing session_created %q: %w", out, err)
	}
	return ts, nil
}

// CurrentSessionName returns the tmux session name for the current process.
// Uses TMUX_PANE for precise targeting — without it, display-message can
// return an arbitrary session when multiple sessions share a socket.
// Returns empty string if not in tmux.
func CurrentSessionName() string {
	tmuxEnv := os.Getenv("TMUX")
	if tmuxEnv == "" {
		return ""
	}
	// Prefer TMUX_PANE (e.g., "%5") for precise targeting. Without -t,
	// display-message returns the most recently active session, which
	// may not be ours when multiple sessions share the default socket.
	pane := os.Getenv("TMUX_PANE")
	var out []byte
	var err error
	if pane != "" {
		out, err = exec.Command("tmux", "display-message", "-t", pane, "-p", "#{session_name}").Output()
	} else {
		out, err = exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CleanupOrphanedSessions scans for zombie Gas Town sessions and kills them.
// A zombie session is one where tmux is alive but the Claude process has died.
// This runs at `gt start` time to prevent session name conflicts and resource accumulation.
//
// The isGTSession predicate identifies Gas Town sessions (e.g. runtime.IsKnownSession).
// It is passed as a parameter to avoid a circular import from tmux → session.
//
// Returns:
//   - cleaned: number of zombie sessions that were killed
//   - err: error if session listing failed (individual kill errors are logged but not returned)
func (t *Tmux) CleanupOrphanedSessions(isGTSession func(string) bool) (cleaned int, err error) {
	sessions, err := t.ListSessions()
	if err != nil {
		return 0, fmt.Errorf("listing sessions: %w", err)
	}

	for _, sess := range sessions {
		// Only process Gas Town sessions
		if !isGTSession(sess) {
			continue
		}

		// Check if the session is a zombie (tmux alive, agent dead)
		if !t.IsAgentAlive(sess) {
			// Kill the zombie session
			if killErr := t.KillSessionWithProcesses(sess); killErr != nil {
				// Log but continue - other sessions may still need cleanup
				fmt.Printf("  warning: failed to kill orphaned session %s: %v\n", sess, killErr)
				continue
			}
			cleaned++
		}
	}

	return cleaned, nil
}

// SetPaneDiedHook sets a pane-died hook on a session to detect crashes.
// When the pane exits, tmux runs the hook command with exit status info.
// The agentID is used to identify the agent in crash logs (e.g., "gastown/Toast").
func (t *Tmux) SetPaneDiedHook(session, agentID string) error {
	if err := validateSessionName(session); err != nil {
		return err
	}
	// Sanitize agentID to prevent shell injection (session already validated by regex)
	agentID = strings.ReplaceAll(agentID, "'", "'\\''")
	session = strings.ReplaceAll(session, "'", "'\\''") // safe after validation, but keep for consistency

	// Hook command logs the crash with exit status
	// #{pane_dead_status} is the exit code of the process that died
	// We run gt log crash which records to the town log
	hookCmd := fmt.Sprintf(`run-shell "gt log crash --agent '%s' --session '%s' --exit-code #{pane_dead_status}"`,
		agentID, session)

	// Set the hook on this specific session
	_, err := t.run("set-hook", "-t", session, "pane-died", hookCmd)
	return err
}

// SetAutoRespawnHook configures a session to automatically respawn when the pane dies.
// This is used for persistent agents like Deacon that should never exit.
// PATCH-010: Fixes Deacon crash loop by respawning at tmux level.
//
// The hook:
// 1. Waits 3 seconds (debounce rapid crashes)
// 2. Respawns the pane with its original command
// 3. Re-enables remain-on-exit (respawn-pane resets it to off!)
//
// Requires remain-on-exit to be set first (called automatically by this function).
func (t *Tmux) SetAutoRespawnHook(session string) error {
	if err := validateSessionName(session); err != nil {
		return err
	}
	// First, enable remain-on-exit so the pane stays after process exit
	if err := t.SetRemainOnExit(session, true); err != nil {
		return fmt.Errorf("setting remain-on-exit: %w", err)
	}

	// Sanitize session name for shell safety
	safeSession := strings.ReplaceAll(session, "'", "'\\''")

	// Hook command: wait, respawn, then re-enable remain-on-exit
	// IMPORTANT: respawn-pane automatically resets remain-on-exit to off!
	// We must re-enable it after each respawn for continuous recovery.
	// The sleep prevents rapid respawn loops if Claude crashes immediately.
	hookCmd := fmt.Sprintf(`run-shell "sleep 3 && tmux respawn-pane -k -t '%s' && tmux set-option -t '%s' remain-on-exit on"`, safeSession, safeSession)

	// Set the hook on this specific session
	_, err := t.run("set-hook", "-t", session, "pane-died", hookCmd)
	if err != nil {
		return fmt.Errorf("setting pane-died hook: %w", err)
	}

	return nil
}
