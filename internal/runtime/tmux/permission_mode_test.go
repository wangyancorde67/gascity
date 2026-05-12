package tmux

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestPermissionModeCapabilityRequiresReadablePaneState(t *testing.T) {
	fe := &fakeExecutor{
		outs: []string{"plain prompt without mode"},
	}
	tm := NewTmux()
	tm.exec = fe
	provider := permissionModeTestProvider(tm, "sess")

	capability := provider.PermissionModeCapability("sess", "claude")
	if capability.Supported {
		t.Fatalf("capability supported = true, want false: %+v", capability)
	}
	if capability.LiveSwitch {
		t.Fatalf("capability live_switch = true, want false: %+v", capability)
	}
	if len(capability.Values) != 0 {
		t.Fatalf("capability values = %v, want none", capability.Values)
	}
	if capability.Reason == "" {
		t.Fatalf("capability reason is empty")
	}
}

func TestPermissionModeCapabilityAdvertisesOnlyCurrentCycle(t *testing.T) {
	fe := &fakeExecutor{
		outs: []string{"Shift+Tab to cycle permission mode: Accept Edits mode"},
	}
	tm := NewTmux()
	tm.exec = fe
	provider := permissionModeTestProvider(tm, "sess")

	capability := provider.PermissionModeCapability("sess", "claude")
	want := []runtime.PermissionMode{
		runtime.PermissionModeDefault,
		runtime.PermissionModeAcceptEdits,
		runtime.PermissionModePlan,
		runtime.PermissionModeBypassPermissions,
	}
	if !capability.Supported || !capability.Readable || !capability.LiveSwitch {
		t.Fatalf("capability = %+v, want supported readable live_switch", capability)
	}
	if !reflect.DeepEqual(capability.Values, want) {
		t.Fatalf("capability values = %v, want %v", capability.Values, want)
	}
}

func TestPermissionModeCycleRejectsUnadvertisedTarget(t *testing.T) {
	steps, ok := runtime.PermissionModeCycleSteps(runtime.PermissionModeAcceptEdits, runtime.PermissionModeBypassPermissions)
	if !ok || steps != 2 {
		t.Fatalf("acceptEdits to bypassPermissions = %d, %v; want 2, true", steps, ok)
	}
	steps, ok = runtime.PermissionModeCycleSteps(runtime.PermissionModeBypassPermissions, runtime.PermissionModeAcceptEdits)
	if !ok || steps != 2 {
		t.Fatalf("bypassPermissions to acceptEdits = %d, %v; want 2, true", steps, ok)
	}
}

func TestParseClaudePermissionModeAcceptsCompactFooterLabels(t *testing.T) {
	tests := []struct {
		name string
		pane string
		want runtime.PermissionMode
	}{
		{name: "default", pane: "\nDefault\n", want: runtime.PermissionModeDefault},
		{name: "normal", pane: "\nNormal\n", want: runtime.PermissionModeDefault},
		{name: "accept edits", pane: "\nAccept Edits\n", want: runtime.PermissionModeAcceptEdits},
		{name: "plan", pane: "\nPlan\n", want: runtime.PermissionModePlan},
		{name: "bypass permissions", pane: "\nBypass Permissions\n", want: runtime.PermissionModeBypassPermissions},
		{name: "boxed label", pane: "\n| Accept Edits |\n", want: runtime.PermissionModeAcceptEdits},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseClaudePermissionMode(tt.pane)
			if !ok || got != tt.want {
				t.Fatalf("parseClaudePermissionMode(%q) = %q, %v; want %q, true", tt.pane, got, ok, tt.want)
			}
		})
	}
}

func TestParseClaudePermissionModeRejectsProseWithoutFooterContext(t *testing.T) {
	tests := []string{
		"we need a plan",
		"please accept edits before submitting",
		"default branch is main",
	}
	for _, pane := range tests {
		t.Run(pane, func(t *testing.T) {
			if got, ok := parseClaudePermissionMode(pane); ok {
				t.Fatalf("parseClaudePermissionMode(%q) = %q, true; want unsupported", pane, got)
			}
		})
	}
}

func TestPermissionModeCycleSendsBackTab(t *testing.T) {
	fe := &fakeExecutor{}
	tm := NewTmux()
	tm.exec = fe
	provider := &Provider{tm: tm}

	if err := provider.sendClaudePermissionModeCycleKey("sess"); err != nil {
		t.Fatalf("send cycle key: %v", err)
	}
	want := []string{"-u", "send-keys", "-t", "sess", "BTab"}
	if len(fe.calls) != 1 || !reflect.DeepEqual(fe.calls[0], want) {
		t.Fatalf("tmux calls = %v, want %v", fe.calls, want)
	}
}

func TestSetPermissionModeReturnsUnverifiedWhenPostSwitchReadUnsupported(t *testing.T) {
	withFastPermissionModeVerification(t)
	fe := &fakeExecutor{
		outs: []string{
			"Shift+Tab to cycle permission mode: Default mode",
			"",
			"plain prompt without mode footer",
		},
	}
	tm := NewTmux()
	tm.exec = fe
	provider := permissionModeTestProvider(tm, "sess")

	state, err := provider.SetPermissionMode(context.Background(), "sess", "claude", runtime.PermissionModeAcceptEdits)
	if err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	if state.Mode != runtime.PermissionModeAcceptEdits || state.Verified {
		t.Fatalf("state = %+v, want acceptEdits with verified=false", state)
	}
}

func TestSetPermissionModePollsUntilPostSwitchReadConfirmsTarget(t *testing.T) {
	withFastPermissionModeVerification(t)
	fe := &fakeExecutor{
		outs: []string{
			"Shift+Tab to cycle permission mode: Default mode",
			"",
			"plain prompt while footer redraws",
			"Shift+Tab to cycle permission mode: Accept Edits mode",
		},
	}
	tm := NewTmux()
	tm.exec = fe
	provider := permissionModeTestProvider(tm, "sess")

	state, err := provider.SetPermissionMode(context.Background(), "sess", "claude", runtime.PermissionModeAcceptEdits)
	if err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	if state.Mode != runtime.PermissionModeAcceptEdits || !state.Verified {
		t.Fatalf("state = %+v, want verified acceptEdits", state)
	}
}

func TestSetPermissionModePollsPastStalePostSwitchMode(t *testing.T) {
	withFastPermissionModeVerification(t)
	fe := &fakeExecutor{
		outs: []string{
			"Shift+Tab to cycle permission mode: Accept Edits mode",
			"",
			"Shift+Tab to cycle permission mode: Accept Edits mode",
			"Shift+Tab to cycle permission mode: Plan mode",
		},
	}
	tm := NewTmux()
	tm.exec = fe
	provider := permissionModeTestProvider(tm, "sess")

	state, err := provider.SetPermissionMode(context.Background(), "sess", "claude", runtime.PermissionModePlan)
	if err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	if state.Mode != runtime.PermissionModePlan || !state.Verified {
		t.Fatalf("state = %+v, want verified plan", state)
	}
}

func withFastPermissionModeVerification(t *testing.T) {
	t.Helper()
	oldCycleDelay := permissionModeCycleKeyDelay
	oldSettleDelay := permissionModePostSwitchSettleDelay
	oldPollInterval := permissionModePostSwitchPollInterval
	oldPollTimeout := permissionModePostSwitchPollTimeout
	permissionModeCycleKeyDelay = time.Millisecond
	permissionModePostSwitchSettleDelay = time.Millisecond
	permissionModePostSwitchPollInterval = time.Millisecond
	permissionModePostSwitchPollTimeout = 5 * time.Millisecond
	t.Cleanup(func() {
		permissionModeCycleKeyDelay = oldCycleDelay
		permissionModePostSwitchSettleDelay = oldSettleDelay
		permissionModePostSwitchPollInterval = oldPollInterval
		permissionModePostSwitchPollTimeout = oldPollTimeout
	})
}

type permissionModeStaticFetcher map[string]bool

func (f permissionModeStaticFetcher) FetchRunning(context.Context) (map[string]bool, error) {
	out := make(map[string]bool, len(f))
	for name, running := range f {
		out[name] = running
	}
	return out, nil
}

func permissionModeTestProvider(tm *Tmux, runningNames ...string) *Provider {
	running := make(permissionModeStaticFetcher, len(runningNames))
	for _, name := range runningNames {
		running[name] = true
	}
	return &Provider{
		tm:    tm,
		cache: NewStateCache(running, time.Hour),
	}
}
