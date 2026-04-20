package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/overlay"
	"github.com/gastownhall/gascity/internal/runtime"
)

// Provider adapts [Tmux] to the [runtime.Provider] interface.
type Provider struct {
	tm       *Tmux
	cfg      Config
	cache    *StateCache
	mu       sync.Mutex
	workDirs map[string]string // session name → workDir (for CopyTo)
}

var instanceTokenReader = rand.Reader

// Compile-time check.
var (
	_ runtime.Provider                      = (*Provider)(nil)
	_ runtime.ImmediateNudgeProvider        = (*Provider)(nil)
	_ runtime.InterruptBoundaryWaitProvider = (*Provider)(nil)
	_ runtime.InterruptedTurnResetProvider  = (*Provider)(nil)
)

// NewProvider returns a [Provider] backed by a real tmux installation
// with default configuration.
func NewProvider() *Provider {
	return NewProviderWithConfig(DefaultConfig())
}

// NewProviderWithConfig returns a [Provider] with the given configuration.
func NewProviderWithConfig(cfg Config) *Provider {
	tm := NewTmuxWithConfig(cfg)
	ttl := cacheTTLFromEnv()
	return &Provider{
		tm:       tm,
		cfg:      cfg,
		cache:    NewStateCache(&tmuxFetcher{tm: tm}, ttl),
		workDirs: make(map[string]string),
	}
}

// Start creates a new detached tmux session and performs a multi-step
// startup sequence to ensure agent readiness. The sequence handles zombie
// detection, command launch verification, permission warning dismissal,
// and runtime readiness polling. Steps are conditional on Config fields
// being set; an agent with no startup hints gets fire-and-forget.
func (p *Provider) Start(ctx context.Context, name string, cfg runtime.Config) error {
	success := false
	defer func() {
		if !success {
			p.clearWorkDir(name)
		}
	}()

	var err error
	cfg.Env, err = ensureInstanceToken(cfg.Env)
	if err != nil {
		return fmt.Errorf("ensuring instance token: %w", err)
	}
	cfg.Env = injectSessionRuntimeHintsEnv(cfg.Env, cfg)

	// Copy overlays and CopyFiles before creating the tmux session.
	// Local provider: files are on the same filesystem.
	// V2 per-provider overlay support: CopyDirForProviders copies universal
	// files then per-provider/<provider>/ slots for ProviderName plus any
	// InstallAgentHooks entries (flattened).
	overlayProviders := append([]string{cfg.ProviderName}, cfg.InstallAgentHooks...)
	if cfg.WorkDir != "" {
		for _, od := range cfg.PackOverlayDirs {
			if err := overlay.CopyDirForProviders(od, cfg.WorkDir, overlayProviders, io.Discard); err != nil {
				return fmt.Errorf("copying pack overlay %s: %w", od, err)
			}
		}
	}
	// Agent-level overlay (highest priority; merges known settings files, overwrites others).
	if cfg.OverlayDir != "" && cfg.WorkDir != "" {
		if err := overlay.CopyDirForProviders(cfg.OverlayDir, cfg.WorkDir, overlayProviders, io.Discard); err != nil {
			return fmt.Errorf("copying overlay %s: %w", cfg.OverlayDir, err)
		}
	}
	for _, cf := range cfg.CopyFiles {
		dst := cfg.WorkDir
		if cf.RelDst != "" {
			dst = filepath.Join(cfg.WorkDir, cf.RelDst)
		}
		// Skip if src and dst are the same path.
		if absSrc, err := filepath.Abs(cf.Src); err == nil {
			if absDst, err := filepath.Abs(dst); err == nil && absSrc == absDst {
				continue
			}
		}
		if err := overlay.CopyFileOrDir(cf.Src, dst, io.Discard); err != nil {
			return fmt.Errorf("copying file %s to %s: %w", cf.Src, dst, err)
		}
	}

	err = doStartSession(ctx, &tmuxStartOps{tm: p.tm}, name, cfg, p.cfg.SetupTimeout, newStartupReporter(os.Stderr))
	if err == nil {
		p.setWorkDir(name, cfg.WorkDir)
		p.cache.Invalidate()
		success = true
		return nil
	}
	p.cleanupFailedStart(name, cfg)
	return err
}

func ensureInstanceToken(env map[string]string) (map[string]string, error) {
	cloned := make(map[string]string, len(env)+1)
	for k, v := range env {
		cloned[k] = v
	}
	if strings.TrimSpace(cloned["GC_INSTANCE_TOKEN"]) == "" {
		token, err := newInstanceToken()
		if err != nil {
			return nil, err
		}
		cloned["GC_INSTANCE_TOKEN"] = token
	}
	return cloned, nil
}

func injectSessionRuntimeHintsEnv(env map[string]string, cfg runtime.Config) map[string]string {
	cloned := make(map[string]string, len(env)+1)
	for k, v := range env {
		cloned[k] = v
	}
	if prompt := strings.TrimSpace(cfg.ReadyPromptPrefix); prompt != "" {
		cloned[sessionReadyPromptEnvKey] = cfg.ReadyPromptPrefix
	} else {
		delete(cloned, sessionReadyPromptEnvKey)
	}
	return cloned
}

func newInstanceToken() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(instanceTokenReader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (p *Provider) cleanupFailedStart(name string, cfg runtime.Config) {
	p.clearWorkDir(name)

	instanceToken := strings.TrimSpace(cfg.Env["GC_INSTANCE_TOKEN"])
	if instanceToken == "" {
		// Best-effort safety guard: only managed session starts carry the
		// instance token we can use to prove ownership before killing by name.
		return
	}
	liveToken, err := p.tm.GetEnvironment(name, "GC_INSTANCE_TOKEN")
	if err != nil {
		return
	}
	if strings.TrimSpace(liveToken) != instanceToken {
		return
	}
	if err := p.tm.KillSessionWithProcesses(name); err == nil {
		p.cache.Invalidate()
	}
}

// RunLive re-applies session_live commands to a running session.
// Called by the reconciler when only session_live config has changed.
func (p *Provider) RunLive(name string, cfg runtime.Config) error {
	runSessionLive(context.Background(), &tmuxStartOps{tm: p.tm}, name, cfg, p.cfg.SetupTimeout, newStartupReporter(os.Stderr))
	return nil
}

// Stop destroys the named session and kills its entire process tree.
// Returns nil if it doesn't exist (idempotent).
// Invalidates the state cache after a successful stop so subsequent
// IsRunning calls see the updated state immediately.
func (p *Provider) Stop(name string) error {
	p.tm.CloseHiddenAttachClient(name)
	err := p.tm.KillSessionWithProcesses(name)
	if err != nil && (errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer)) {
		p.clearWorkDir(name)
		p.cache.EvictSession(name)
		return nil // idempotent
	}
	if err == nil {
		// Immediately remove from cache so IsRunning reflects the kill
		// without waiting for an async refresh cycle.
		p.clearWorkDir(name)
		p.cache.EvictSession(name)
	}
	return err
}

// Interrupt sends Ctrl-C to the named tmux session.
// Best-effort: returns nil if the session doesn't exist.
func (p *Provider) Interrupt(name string) error {
	if p.tm.requiresHiddenAttachedInterrupt(name) && !p.tm.IsSessionAttached(name) {
		if err := p.tm.ensureHiddenAttachedClient(name); err != nil {
			return fmt.Errorf("preparing detached gemini interrupt: %w", err)
		}
	}
	if used, err := p.tm.sendHiddenAttachedKeys(name, "C-c"); used {
		if err != nil {
			return err
		}
		return nil
	}
	err := p.tm.SendKeysRaw(name, "C-c")
	if err != nil && (errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer)) {
		return nil
	}
	return err
}

// IsRunning reports whether the named session has a live (non-dead) pane.
// Uses a short-lived cache (default 2s TTL) backed by a single
// `tmux list-panes -a` call instead of per-session HasSession + IsPaneDead
// subprocess calls. Sessions with remain-on-exit corpses (pane_dead=1)
// are correctly excluded — only sessions with live panes are "running".
func (p *Provider) IsRunning(name string) bool {
	return p.cache.IsRunning(name)
}

// IsAttached reports whether a user terminal is connected to the named session.
func (p *Provider) IsAttached(name string) bool {
	return p.tm.IsSessionAttached(name)
}

// ProcessAlive reports whether the named session has a live agent
// process matching one of the given names in its process tree.
// Returns true if processNames is empty (no check possible).
func (p *Provider) ProcessAlive(name string, processNames []string) bool {
	if len(processNames) == 0 {
		return true
	}
	return p.tm.IsRuntimeRunning(name, processNames)
}

// Capabilities reports tmux provider capabilities.
// Tmux supports both attachment detection and activity reporting.
func (p *Provider) Capabilities() runtime.ProviderCapabilities {
	return runtime.ProviderCapabilities{
		CanReportAttachment: true,
		CanReportActivity:   true,
	}
}

// SleepCapability reports that tmux supports full idle sleep semantics.
func (p *Provider) SleepCapability(string) runtime.SessionSleepCapability {
	return runtime.SessionSleepCapabilityFull
}

// WaitForIdle waits for the named session to reach an idle prompt.
func (p *Provider) WaitForIdle(ctx context.Context, name string, timeout time.Duration) error {
	return p.tm.WaitForIdle(ctx, name, timeout)
}

// WaitForInterruptBoundary waits for a provider-native interrupt acknowledgement
// before the next user turn is injected.
func (p *Provider) WaitForInterruptBoundary(ctx context.Context, name string, since time.Time, timeout time.Duration) error {
	return p.tm.WaitForInterruptBoundary(ctx, name, since, timeout)
}

// ResetInterruptedTurn discards the just-interrupted Gemini user turn without
// restarting the session.
func (p *Provider) ResetInterruptedTurn(ctx context.Context, name string) error {
	if p.tm.requiresHiddenAttachedInterrupt(name) && !p.tm.IsSessionAttached(name) {
		if err := p.tm.ensureHiddenAttachedClient(name); err != nil {
			return fmt.Errorf("preparing detached gemini rewind: %w", err)
		}
	}
	if err := p.NudgeNow(name, runtime.TextContent("/rewind")); err != nil {
		return fmt.Errorf("opening gemini rewind: %w", err)
	}
	if err := p.waitForPane(ctx, name, geminiRewindDialogVisible); err != nil {
		return fmt.Errorf("waiting for gemini rewind picker: %w", err)
	}
	if err := p.SendKeys(name, "Up"); err != nil {
		return fmt.Errorf("selecting interrupted gemini turn: %w", err)
	}
	if err := sleepWithContext(ctx, 100*time.Millisecond); err != nil {
		return err
	}
	if err := p.SendKeys(name, "Enter"); err != nil {
		return fmt.Errorf("opening gemini rewind confirmation: %w", err)
	}
	if err := p.waitForPane(ctx, name, geminiRewindConfirmationVisible); err != nil {
		return fmt.Errorf("waiting for gemini rewind confirmation: %w", err)
	}
	pane, err := p.tm.CapturePane(name, 80)
	if err != nil {
		return fmt.Errorf("capturing gemini rewind confirmation: %w", err)
	}
	if !strings.Contains(pane, "No code changes to revert.") {
		if err := p.SendKeys(name, "Down"); err != nil {
			return fmt.Errorf("choosing gemini rewind-only action: %w", err)
		}
		if err := sleepWithContext(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	if err := p.SendKeys(name, "Enter"); err != nil {
		return fmt.Errorf("confirming gemini rewind: %w", err)
	}
	if err := p.waitForPane(ctx, name, geminiRewindComplete); err != nil {
		return fmt.Errorf("waiting for gemini rewind completion: %w", err)
	}
	if err := p.tm.WaitForIdle(ctx, name, 10*time.Second); err != nil {
		return fmt.Errorf("waiting for gemini prompt after rewind: %w", err)
	}
	return nil
}

// DismissKnownDialogs best-effort clears known trust/permissions dialogs on a
// running session using a bounded timeout.
func (p *Provider) DismissKnownDialogs(ctx context.Context, name string, timeout time.Duration) error {
	return p.tm.DismissKnownDialogs(ctx, name, timeout)
}

// Nudge sends a message to the named session to wake or redirect the agent.
// By default, waits for the agent to be idle before sending (wait-idle mode)
// to avoid interrupting active tool calls. If the agent doesn't become idle
// within NudgeIdleTimeout, sends immediately as a fallback.
// Delegates to [Tmux.NudgeSession] which handles per-session locking,
// multi-pane resolution, retry with backoff, and SIGWINCH wake.
// Best-effort: returns nil if the session doesn't exist.
func (p *Provider) Nudge(name string, content []runtime.ContentBlock) error {
	// Wait for the agent to be idle before sending, unless disabled.
	// This prevents interrupting active tool calls — the prompt is visible
	// in scrollback during inter-tool-call gaps, so immediate send-keys
	// would inject text mid-execution. See upstream dfd945e9/6bc898ce.
	if idleTimeout := p.tm.cfg.NudgeIdleTimeout; idleTimeout > 0 {
		// Best-effort wait — if it fails (session gone, timeout), proceed
		// with the nudge anyway. The message may arrive during active work,
		// but Claude's cooperative queue will handle it at the next turn.
		_ = p.tm.WaitForIdle(context.Background(), name, idleTimeout)
	}
	return p.NudgeNow(name, content)
}

// NudgeNow sends a message immediately without performing a wait-idle check.
func (p *Provider) NudgeNow(name string, content []runtime.ContentBlock) error {
	var parts []string
	for _, b := range content {
		switch b.Type {
		case "file_path":
			if b.Path != "" {
				base := filepath.Base(b.Path)
				if _, err := os.Stat(b.Path); err != nil {
					parts = append(parts, "[File not found: ./"+base+"]")
				} else if err := p.CopyTo(name, b.Path, base); err != nil {
					parts = append(parts, "[File staging failed: ./"+base+": "+err.Error()+"]")
				} else {
					parts = append(parts, "[File staged: ./"+base+"]")
				}
			}
		default: // "text"
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
	}
	message := strings.Join(parts, "\n")
	if message == "" {
		return nil
	}

	if used, err := p.tm.sendHiddenAttachedText(name, message); used {
		if err != nil {
			return err
		}
		return nil
	}

	err := p.tm.NudgeSession(name, message)
	if err != nil && (errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer)) {
		return nil
	}
	return err
}

// SetMeta stores a key-value pair in the named session's tmux environment.
func (p *Provider) SetMeta(name, key, value string) error {
	return p.tm.SetEnvironment(name, key, value)
}

// GetMeta retrieves a value from the named session's tmux environment.
// Returns ("", nil) if the key is not set. Propagates session-not-found
// and no-server errors so callers can distinguish "key absent" from
// "session gone."
func (p *Provider) GetMeta(name, key string) (string, error) {
	val, err := p.tm.GetEnvironment(name, key)
	if err != nil {
		switch {
		case errors.Is(err, ErrEnvironmentNotSet):
			return "", nil
		case errors.Is(err, ErrSessionNotFound), errors.Is(err, ErrNoServer):
			return "", err
		default:
			return "", err
		}
	}
	return val, nil
}

// RemoveMeta removes a key from the named session's tmux environment.
func (p *Provider) RemoveMeta(name, key string) error {
	return p.tm.RemoveEnvironment(name, key)
}

// Peek captures the last N lines of output from the named session.
// If lines <= 0, captures all available scrollback.
func (p *Provider) Peek(name string, lines int) (string, error) {
	if lines <= 0 {
		return p.tm.CapturePaneAll(name)
	}
	return p.tm.CapturePane(name, lines)
}

// ListRunning returns all tmux session names matching the given prefix.
func (p *Provider) ListRunning(prefix string) ([]string, error) {
	all, err := p.tm.ListSessions()
	if err != nil {
		return nil, err
	}
	var matched []string
	for _, name := range all {
		if strings.HasPrefix(name, prefix) {
			matched = append(matched, name)
		}
	}
	return matched, nil
}

// GetLastActivity returns the time of the last I/O activity in the named
// session. Delegates to [Tmux.GetSessionActivity].
func (p *Provider) GetLastActivity(name string) (time.Time, error) {
	return p.tm.GetSessionActivity(name)
}

// ClearScrollback clears the scrollback history of the named session.
// Delegates to [Tmux.ClearHistory].
func (p *Provider) ClearScrollback(name string) error {
	return p.tm.ClearHistory(name)
}

func (p *Provider) waitForPane(ctx context.Context, name string, match func(string) bool) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		pane, err := p.tm.CapturePane(name, 80)
		if err == nil && match(pane) {
			return nil
		}
		if err := sleepWithContext(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	return ErrIdleTimeout
}

func geminiRewindDialogVisible(pane string) bool {
	return strings.Contains(pane, "Cancel rewind and stay here") || strings.Contains(pane, "> Rewind")
}

func geminiRewindConfirmationVisible(pane string) bool {
	return strings.Contains(pane, "Confirm Rewind")
}

func geminiRewindComplete(pane string) bool {
	return !strings.Contains(pane, "Confirm Rewind") &&
		!strings.Contains(pane, "Cancel rewind and stay here") &&
		!strings.Contains(pane, "> Rewind") &&
		!strings.Contains(pane, "Rewinding...")
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// SendKeys sends bare keystrokes to the named session. Each key is sent
// as a separate tmux send-keys invocation (e.g., "Enter", "Down", "C-c").
// Best-effort: returns nil if the session doesn't exist.
func (p *Provider) SendKeys(name string, keys ...string) error {
	if used, err := p.tm.sendHiddenAttachedKeys(name, keys...); used {
		if err != nil {
			return err
		}
		return nil
	}
	for _, k := range keys {
		err := p.tm.SendKeysRaw(name, k)
		if err != nil && (errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer)) {
			return nil // best-effort
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CopyTo copies src into the named session's working directory at relDst.
// Best-effort: returns nil if session unknown or src missing.
func (p *Provider) CopyTo(name, src, relDst string) error {
	wd := p.workDir(name)
	if wd == "" {
		return nil // unknown session
	}
	if _, err := os.Stat(src); err != nil {
		return nil // src missing
	}
	dst := wd
	if relDst != "" {
		dst = filepath.Join(wd, relDst)
	}
	return overlay.CopyFileOrDir(src, dst, io.Discard)
}

func (p *Provider) setWorkDir(name, workDir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.TrimSpace(workDir) == "" {
		delete(p.workDirs, name)
		return
	}
	p.workDirs[name] = workDir
}

func (p *Provider) clearWorkDir(name string) {
	p.setWorkDir(name, "")
}

func (p *Provider) workDir(name string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.workDirs[name]
}

// Attach connects the user's terminal to the named tmux session.
// This hands stdin/stdout/stderr to tmux and blocks until detach.
func (p *Provider) Attach(name string) error {
	args := []string{"-u"}
	if p.cfg.SocketName != "" {
		args = append(args, "-L", p.cfg.SocketName)
	}
	args = append(args, "attach-session", "-t", name)
	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Tmux returns the underlying [Tmux] instance for advanced operations
// that are not part of the [runtime.Provider] interface.
func (p *Provider) Tmux() *Tmux {
	return p.tm
}
