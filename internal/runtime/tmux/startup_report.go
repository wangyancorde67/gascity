package tmux

import (
	"fmt"
	"io"
)

type startupReporter struct {
	stderr io.Writer
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
	if r == nil || r.stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(r.stderr, format, args...)
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
