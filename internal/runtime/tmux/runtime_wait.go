package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gastownhall/gascity/internal/runtime"
)

// DefaultReadyPromptPrefix is the Claude Code prompt prefix used for idle detection.
// Claude Code uses ❯ (U+276F) as the prompt character.
const (
	DefaultReadyPromptPrefix = "❯ "
	sessionReadyPromptEnvKey = "GC_READY_PROMPT_PREFIX"
	// promptObservationLines widens prompt detection beyond the pane footer.
	// Claude's welcome/idle UI can leave several blank rows below the prompt,
	// so capturing only the last handful of lines misses the ready indicator.
	promptObservationLines = 120
	// codexInterruptBoundaryTailBytes is the transcript tail window scanned for
	// Codex's durable interrupt acknowledgement marker.
	codexInterruptBoundaryTailBytes = 16 * 1024
	// codexInterruptBoundaryRecentLines limits detection to the newest transcript
	// entries so an older interrupt marker does not satisfy a later interrupt.
	codexInterruptBoundaryRecentLines = 12
)

// WaitForCommand waits until the pane is no longer running one of the excluded commands.
func (t *Tmux) WaitForCommand(ctx context.Context, session string, excludeCommands []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		cmd, err := t.GetPaneCommand(session)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval):
			}
			continue
		}

		excluded := false
		for _, exc := range excludeCommands {
			if cmd == exc {
				excluded = true
				break
			}
		}
		if !excluded || t.IsAgentAlive(session) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for command (still running excluded command)")
}

// WaitForShellReady polls until the pane is running a shell command.
func (t *Tmux) WaitForShellReady(session string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd, err := t.GetPaneCommand(session)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}
		for _, shell := range supportedShells {
			if cmd == shell {
				return nil
			}
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("timeout waiting for shell")
}

// matchesPromptPrefix reports whether a captured pane line matches the
// configured ready-prompt prefix.
func matchesPromptPrefix(line, readyPromptPrefix string) bool {
	if readyPromptPrefix == "" {
		return false
	}
	trimmed := strings.TrimSpace(line)
	trimmed = strings.ReplaceAll(trimmed, "\u00a0", " ")
	normalizedPrefix := strings.ReplaceAll(readyPromptPrefix, "\u00a0", " ")
	prefix := strings.TrimSpace(normalizedPrefix)
	return strings.HasPrefix(trimmed, normalizedPrefix) || (prefix != "" && trimmed == prefix)
}

// WaitForRuntimeReady polls until the agent runtime's ready prompt appears in the pane.
func (t *Tmux) WaitForRuntimeReady(ctx context.Context, session string, rc *RuntimeConfig, timeout time.Duration) error {
	if rc == nil || rc.Tmux == nil {
		return nil
	}

	if rc.Tmux.ReadyPromptPrefix == "" {
		if rc.Tmux.ReadyDelayMs <= 0 {
			return nil
		}
		delay := time.Duration(rc.Tmux.ReadyDelayMs) * time.Millisecond
		if delay > timeout {
			delay = timeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		return nil
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lines, err := t.CapturePaneLines(session, promptObservationLines)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		for _, line := range lines {
			if matchesPromptPrefix(line, rc.Tmux.ReadyPromptPrefix) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for runtime prompt")
}

func idlePromptPrefix(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return DefaultReadyPromptPrefix
}

// WaitForIdle polls until the agent appears to be at an idle prompt.
func (t *Tmux) WaitForIdle(ctx context.Context, session string, timeout time.Duration) error {
	promptPrefix := DefaultReadyPromptPrefix
	if configured, err := t.GetEnvironment(session, sessionReadyPromptEnvKey); err == nil {
		promptPrefix = idlePromptPrefix(configured)
	}
	prefix := strings.TrimSpace(promptPrefix)

	consecutiveIdle := 0
	const requiredConsecutive = 2

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		lines, err := t.CapturePaneLines(session, promptObservationLines)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
				return err
			}
			consecutiveIdle = 0
			if err := waitForIdlePoll(ctx); err != nil {
				return err
			}
			continue
		}

		if paneContainsBusyIndicator(lines) {
			consecutiveIdle = 0
			if err := waitForIdlePoll(ctx); err != nil {
				return err
			}
			continue
		}

		foundPrompt := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if matchesPromptPrefix(trimmed, promptPrefix) || (prefix != "" && trimmed == prefix) {
				foundPrompt = true
				break
			}
		}

		if foundPrompt {
			consecutiveIdle++
			if consecutiveIdle >= requiredConsecutive {
				return nil
			}
		} else {
			consecutiveIdle = 0
		}
		if err := waitForIdlePoll(ctx); err != nil {
			return err
		}
	}
	return ErrIdleTimeout
}

func waitForIdlePoll(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errCodexTranscriptWatchUnavailable = errors.New("codex transcript watch unavailable")

// WaitForInterruptBoundary waits for a provider-native interrupt acknowledgement.
func (t *Tmux) WaitForInterruptBoundary(ctx context.Context, session string, since time.Time, timeout time.Duration) error {
	provider, _ := t.GetEnvironment(session, "GC_PROVIDER")
	switch strings.TrimSpace(provider) {
	case "", "codex":
	default:
		return runtime.ErrInteractionUnsupported
	}
	if strings.TrimSpace(provider) == "" && !t.targetLooksLikeProvider(session, "codex") {
		return runtime.ErrInteractionUnsupported
	}
	codexHome, err := t.GetEnvironment(session, "CODEX_HOME")
	if err != nil {
		return runtime.ErrInteractionUnsupported
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return runtime.ErrInteractionUnsupported
	}
	return waitForCodexInterruptBoundary(ctx, codexHome, since, timeout)
}

func waitForCodexInterruptBoundaryWatch(ctx context.Context, codexHome string, since time.Time, timeout time.Duration) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errCodexTranscriptWatchUnavailable
	}
	defer watcher.Close() //nolint:errcheck

	if err := addCodexTranscriptWatchTree(watcher, codexHome); err != nil {
		return errCodexTranscriptWatchUnavailable
	}

	if found, err := codexInterruptBoundarySatisfied(codexHome, since); err == nil && found {
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrIdleTimeout
		case event, ok := <-watcher.Events:
			if !ok {
				return errCodexTranscriptWatchUnavailable
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					_ = addCodexTranscriptWatchTree(watcher, event.Name)
				}
			}
			if found, err := codexInterruptBoundarySatisfied(codexHome, since); err == nil && found {
				return nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return errCodexTranscriptWatchUnavailable
			}
			_ = err
			return errCodexTranscriptWatchUnavailable
		}
	}
}

func waitForCodexInterruptBoundaryPoll(ctx context.Context, codexHome string, since time.Time, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		transcriptPath, modTime, err := latestCodexTranscriptPath(codexHome)
		if err == nil && !modTime.Before(since) {
			tail, err := readFileTail(transcriptPath, codexInterruptBoundaryTailBytes)
			if err == nil && codexTranscriptTailContainsTurnAborted(tail) {
				return nil
			}
		}
		if err := waitForIdlePoll(ctx); err != nil {
			return err
		}
	}
	return ErrIdleTimeout
}

func waitForCodexInterruptBoundary(ctx context.Context, codexHome string, since time.Time, timeout time.Duration) error {
	if err := waitForCodexInterruptBoundaryWatch(ctx, codexHome, since, timeout); err != nil {
		if errors.Is(err, errCodexTranscriptWatchUnavailable) {
			return waitForCodexInterruptBoundaryPoll(ctx, codexHome, since, timeout)
		}
		return err
	}
	return nil
}

func addCodexTranscriptWatchTree(watcher *fsnotify.Watcher, root string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return errCodexTranscriptWatchUnavailable
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return errCodexTranscriptWatchUnavailable
		}
		if !d.IsDir() {
			return nil
		}
		if addErr := watcher.Add(path); addErr != nil {
			return errCodexTranscriptWatchUnavailable
		}
		return nil
	})
}

func codexInterruptBoundarySatisfied(codexHome string, since time.Time) (bool, error) {
	transcriptPath, modTime, err := latestCodexTranscriptPath(codexHome)
	if err != nil || modTime.Before(since) {
		return false, err
	}
	tail, err := readFileTail(transcriptPath, codexInterruptBoundaryTailBytes)
	if err != nil {
		return false, err
	}
	return codexTranscriptTailContainsTurnAborted(tail), nil
}

func latestCodexTranscriptPath(codexHome string) (string, time.Time, error) {
	root := filepath.Join(codexHome, "sessions")
	var latestPath string
	var latestMod time.Time
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if latestPath == "" || info.ModTime().After(latestMod) {
			latestPath = path
			latestMod = info.ModTime()
		}
		return nil
	})
	if latestPath == "" {
		if err != nil {
			return "", time.Time{}, err
		}
		return "", time.Time{}, os.ErrNotExist
	}
	return latestPath, latestMod, nil
}

func readFileTail(path string, maxBytes int64) (_ string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := info.Size() - maxBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func codexTranscriptTailContainsTurnAborted(tail string) bool {
	lines := strings.Split(strings.TrimSpace(tail), "\n")
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < codexInterruptBoundaryRecentLines; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		seen++
		if !strings.Contains(line, "<turn_aborted>") {
			continue
		}
		if strings.Contains(line, `"role":"user"`) || strings.Contains(line, `"role": "user"`) ||
			strings.Contains(line, `"type":"user_message"`) || strings.Contains(line, `"type": "user_message"`) {
			return true
		}
	}
	return false
}

// paneContainsBusyIndicator checks captured pane lines for signs that the
// agent is actively processing.
func paneContainsBusyIndicator(lines []string) bool {
	for _, line := range lines {
		if strings.Contains(line, "esc to interrupt") ||
			strings.Contains(line, "Press Esc or Ctrl+C to cancel") ||
			strings.Contains(line, "[current working directory ") {
			return true
		}
	}
	return false
}
