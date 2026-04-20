package tmux

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/shellquote"
)

const (
	hiddenAttachReadyTimeout = 2 * time.Second
	hiddenAttachMaxLifetime  = 20 * time.Second
	hiddenAttachPollInterval = 50 * time.Millisecond
)

type hiddenAttachClient struct {
	cancel  context.CancelFunc
	done    chan error
	stdin   io.WriteCloser
	writeMu sync.Mutex
}

// IsSessionAttached returns true if the session has any clients attached.
func (t *Tmux) IsSessionAttached(target string) bool {
	attached, err := t.run("display-message", "-t", target, "-p", "#{session_attached}")
	return err == nil && attached == "1"
}

// WakePane triggers a SIGWINCH in a pane by resizing it slightly then restoring.
func (t *Tmux) WakePane(target string) {
	_, _ = t.run("resize-pane", "-t", target, "-y", "-1")
	time.Sleep(50 * time.Millisecond)
	_, _ = t.run("resize-pane", "-t", target, "-y", "+1")
}

// WakePaneIfDetached triggers a SIGWINCH only if the session is detached.
func (t *Tmux) WakePaneIfDetached(target string) {
	if t.IsSessionAttached(target) {
		return
	}
	t.WakePane(target)
}

func (t *Tmux) providerEnv(target string) string {
	provider, err := t.GetEnvironment(target, "GC_PROVIDER")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(provider)
}

func (t *Tmux) requiresHiddenAttachedInterrupt(target string) bool {
	switch t.providerEnv(target) {
	case "gemini":
		return true
	case "":
		return t.targetLooksLikeProvider(target, "gemini")
	default:
		return false
	}
}

func (t *Tmux) ensureHiddenAttachedClient(target string) error {
	if t.IsSessionAttached(target) {
		return nil
	}

	t.hiddenAttachMu.Lock()
	if client := t.hiddenAttachClients[target]; client != nil {
		t.hiddenAttachMu.Unlock()
		return t.waitForHiddenAttachReady(target, client)
	}

	ctx, cancel := context.WithTimeout(context.Background(), hiddenAttachMaxLifetime)
	cmdArgs := []string{"-u"}
	if t.cfg.SocketName != "" {
		cmdArgs = append(cmdArgs, "-L", t.cfg.SocketName)
	}
	cmdArgs = append(cmdArgs, "attach-session", "-t", target)
	cmd := exec.CommandContext(ctx, "script", "-qfc", "tmux "+shellquote.Join(cmdArgs), "/dev/null")
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.hiddenAttachMu.Unlock()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		t.hiddenAttachMu.Unlock()
		return err
	}
	client := &hiddenAttachClient{
		cancel: cancel,
		done:   make(chan error, 1),
		stdin:  stdin,
	}
	if t.hiddenAttachClients == nil {
		t.hiddenAttachClients = make(map[string]*hiddenAttachClient)
	}
	t.hiddenAttachClients[target] = client
	t.hiddenAttachMu.Unlock()

	go func() {
		err := cmd.Wait()
		_ = stdin.Close()
		client.done <- err
		close(client.done)
		t.clearHiddenAttachClient(target, client)
	}()

	if err := t.waitForHiddenAttachReady(target, client); err != nil {
		t.CloseHiddenAttachClient(target)
		return err
	}
	return nil
}

func (t *Tmux) hiddenAttachClient(target string) *hiddenAttachClient {
	t.hiddenAttachMu.Lock()
	defer t.hiddenAttachMu.Unlock()
	return t.hiddenAttachClients[target]
}

func (t *Tmux) waitForHiddenAttachReady(target string, client *hiddenAttachClient) error {
	deadline := time.Now().Add(hiddenAttachReadyTimeout)
	for time.Now().Before(deadline) {
		if t.IsSessionAttached(target) {
			return nil
		}
		select {
		case err, ok := <-client.done:
			if !ok {
				return fmt.Errorf("hidden tmux client exited before attaching")
			}
			if err != nil {
				return fmt.Errorf("hidden tmux client exited before attaching: %w", err)
			}
			return fmt.Errorf("hidden tmux client exited before attaching")
		default:
		}
		time.Sleep(hiddenAttachPollInterval)
	}
	return fmt.Errorf("timed out waiting for hidden tmux client to attach")
}

func (t *Tmux) clearHiddenAttachClient(target string, client *hiddenAttachClient) {
	t.hiddenAttachMu.Lock()
	defer t.hiddenAttachMu.Unlock()
	if existing := t.hiddenAttachClients[target]; existing == client {
		delete(t.hiddenAttachClients, target)
	}
}

// CloseHiddenAttachClient tears down the short-lived hidden client used to
// make detached Gemini Ctrl-C interrupts behave like a real attached terminal.
func (t *Tmux) CloseHiddenAttachClient(target string) {
	t.hiddenAttachMu.Lock()
	client := t.hiddenAttachClients[target]
	delete(t.hiddenAttachClients, target)
	t.hiddenAttachMu.Unlock()

	if client == nil {
		return
	}
	client.cancel()
	_ = client.stdin.Close()
	select {
	case <-client.done:
	case <-time.After(500 * time.Millisecond):
	}
}

func (c *hiddenAttachClient) write(input []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.stdin.Write(input)
	return err
}

func hiddenAttachedKeyBytes(key string) ([]byte, bool) {
	switch strings.TrimSpace(key) {
	case "C-c":
		return []byte{0x03}, true
	case "C-u":
		return []byte{0x15}, true
	case "Enter":
		return []byte{'\r'}, true
	case "Escape":
		return []byte{0x1b}, true
	case "Up":
		return []byte{0x1b, '[', 'A'}, true
	case "Down":
		return []byte{0x1b, '[', 'B'}, true
	case "Right":
		return []byte{0x1b, '[', 'C'}, true
	case "Left":
		return []byte{0x1b, '[', 'D'}, true
	default:
		return nil, false
	}
}

func (t *Tmux) sendHiddenAttachedKeys(target string, keys ...string) (bool, error) {
	client := t.hiddenAttachClient(target)
	if client == nil {
		return false, nil
	}
	sequences := make([][]byte, 0, len(keys))
	for _, key := range keys {
		seq, ok := hiddenAttachedKeyBytes(key)
		if !ok {
			return false, nil
		}
		sequences = append(sequences, seq)
	}
	for _, seq := range sequences {
		if err := client.write(seq); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (t *Tmux) sendHiddenAttachedText(target, text string) (bool, error) {
	client := t.hiddenAttachClient(target)
	if client == nil {
		return false, nil
	}
	if text == "" {
		return true, nil
	}
	if err := client.write([]byte(text)); err != nil {
		return true, err
	}
	if t.cfg.DebounceMs > 0 {
		time.Sleep(time.Duration(t.cfg.DebounceMs) * time.Millisecond)
	}
	if err := client.write([]byte{'\r'}); err != nil {
		return true, err
	}
	return true, nil
}

// isTransientSendKeysError returns true if the error from tmux send-keys is
// transient and safe to retry.
func isTransientSendKeysError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not in a mode")
}

// sendKeysLiteralWithRetry sends literal text to a tmux target, retrying on
// transient errors (e.g., "not in a mode" during agent TUI startup).
func (t *Tmux) sendKeysLiteralWithRetry(target, text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	interval := t.cfg.NudgeRetryInterval
	var lastErr error

	for time.Now().Before(deadline) {
		_, err := t.run("send-keys", "-t", target, "-l", text)
		if err == nil {
			return nil
		}
		if !isTransientSendKeysError(err) {
			return err
		}
		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
		interval = interval * 3 / 2
		if interval > 2*time.Second {
			interval = 2 * time.Second
		}
	}
	return fmt.Errorf("agent not ready for input after %s: %w", timeout, lastErr)
}

// NudgeSession sends a message to a Claude Code session reliably.
func (t *Tmux) NudgeSession(session, message string) error {
	if !acquireNudgeLock(session, t.cfg.NudgeLockTimeout) {
		return fmt.Errorf("nudge lock timeout for session %q: previous nudge may be hung", session)
	}
	defer releaseNudgeLock(session)

	target := session
	if agentPane, err := t.FindAgentPane(session); err == nil && agentPane != "" {
		target = agentPane
	}

	if err := t.sendKeysLiteralWithRetry(target, message, t.cfg.NudgeReadyTimeout); err != nil {
		return err
	}

	time.Sleep(500 * time.Millisecond)

	if t.shouldSendEscapeBeforeEnter(target) {
		_, _ = t.run("send-keys", "-t", target, "Escape")
		time.Sleep(100 * time.Millisecond)
	}

	t.WakePaneIfDetached(session)

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := t.run("send-keys", "-t", target, "Enter"); err != nil {
			lastErr = err
			continue
		}
		t.WakePaneIfDetached(session)
		return nil
	}
	return fmt.Errorf("failed to send Enter after 3 attempts: %w", lastErr)
}

// NudgePane sends a message to a specific pane reliably.
func (t *Tmux) NudgePane(pane, message string) error {
	if !acquireNudgeLock(pane, t.cfg.NudgeLockTimeout) {
		return fmt.Errorf("nudge lock timeout for pane %q: previous nudge may be hung", pane)
	}
	defer releaseNudgeLock(pane)

	if err := t.sendKeysLiteralWithRetry(pane, message, t.cfg.NudgeReadyTimeout); err != nil {
		return err
	}

	time.Sleep(500 * time.Millisecond)

	if t.shouldSendEscapeBeforeEnter(pane) {
		_, _ = t.run("send-keys", "-t", pane, "Escape")
		time.Sleep(100 * time.Millisecond)
	}

	t.WakePaneIfDetached(pane)

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := t.run("send-keys", "-t", pane, "Enter"); err != nil {
			lastErr = err
			continue
		}
		t.WakePaneIfDetached(pane)
		return nil
	}
	return fmt.Errorf("failed to send Enter after 3 attempts: %w", lastErr)
}

func (t *Tmux) shouldSendEscapeBeforeEnter(target string) bool {
	provider, err := t.GetEnvironment(target, "GC_PROVIDER")
	if err == nil {
		switch strings.TrimSpace(provider) {
		case "claude", "codex", "gemini":
			return false
		}
	}
	if t.targetLooksLikeNoEscapeProvider(target) {
		return false
	}
	return true
}

func (t *Tmux) targetLooksLikeNoEscapeProvider(target string) bool {
	noEscapeProviders := []string{"claude", "codex", "gemini"}
	return t.targetLooksLikeAnyProvider(target, noEscapeProviders...)
}

func (t *Tmux) targetLooksLikeProvider(target, provider string) bool {
	return t.targetLooksLikeAnyProvider(target, provider)
}

func (t *Tmux) targetLooksLikeAnyProvider(target string, providers ...string) bool {
	pid, err := t.GetPanePID(target)
	if err != nil || strings.TrimSpace(pid) == "" {
		return false
	}
	if processMatchesNames(pid, providers) {
		return true
	}
	return hasDescendantWithNames(pid, providers, 0)
}
