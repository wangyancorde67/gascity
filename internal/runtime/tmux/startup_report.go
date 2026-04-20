package tmux

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

type startupReporter struct {
	stderr io.Writer
	mu     sync.Mutex
	warns  []string
}

func newStartupReporter(stderr io.Writer) *startupReporter {
	if stderr == nil {
		stderr = io.Discard
	}
	return &startupReporter{stderr: stderr}
}

func selectStartupReporter(reporters []*startupReporter) *startupReporter {
	if len(reporters) > 0 && reporters[0] != nil {
		return reporters[0]
	}
	return newStartupReporter(io.Discard)
}

func (r *startupReporter) warnf(format string, args ...any) {
	if r == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if r.stderr != nil {
		_, _ = io.WriteString(r.stderr, msg)
	}
	r.mu.Lock()
	r.warns = append(r.warns, strings.TrimSpace(msg))
	r.mu.Unlock()
}

func (r *startupReporter) startupWarning(step string, err error) {
	if err == nil {
		return
	}
	r.warnf("gc: startup %s warning: %v\n", step, err)
}

func (r *startupReporter) sessionSetupWarning(index int, err error) {
	if err == nil {
		return
	}
	r.warnf("gc: session_setup[%d] warning: %v\n", index, err)
}

func (r *startupReporter) sessionSetupScriptWarning(err error) {
	if err == nil {
		return
	}
	r.warnf("gc: session_setup_script warning: %v\n", err)
}

func (r *startupReporter) sessionLiveWarning(index int, err error) {
	if err == nil {
		return
	}
	r.warnf("gc: session_live[%d] warning: %v\n", index, err)
}

func (r *startupReporter) warnings() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.warns))
	copy(out, r.warns)
	return out
}
