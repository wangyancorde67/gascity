package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/telemetry"
	"github.com/spf13/cobra"
)

const (
	defaultQueuedNudgeTTL         = 24 * time.Hour
	defaultQueuedNudgeRetryDelay  = 15 * time.Second
	defaultQueuedNudgeMaxAttempts = 5
	defaultNudgePollInterval      = 2 * time.Second
	defaultNudgePollQuiescence    = 3 * time.Second
	defaultNudgeWaitIdleTimeout   = 30 * time.Second
)

type nudgeDeliveryMode string

const (
	nudgeDeliveryImmediate nudgeDeliveryMode = "immediate"
	nudgeDeliveryWaitIdle  nudgeDeliveryMode = "wait-idle"
	nudgeDeliveryQueue     nudgeDeliveryMode = "queue"
)

type queuedNudge struct {
	ID            string    `json:"id"`
	Agent         string    `json:"agent"`
	Source        string    `json:"source"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"created_at"`
	DeliverAfter  time.Time `json:"deliver_after"`
	ExpiresAt     time.Time `json:"expires_at"`
	Attempts      int       `json:"attempts,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	DeadAt        time.Time `json:"dead_at,omitempty"`
}

type nudgeQueueState struct {
	Pending []queuedNudge `json:"pending,omitempty"`
	Dead    []queuedNudge `json:"dead,omitempty"`
}

type nudgeTarget struct {
	cityPath    string
	cityName    string
	cfg         *config.City
	agent       config.Agent
	resolved    *config.ResolvedProvider
	sessionName string
}

func newNudgeCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nudge",
		Short: "Inspect and deliver deferred nudges",
		Long: `Inspect and deliver deferred nudges.

Deferred nudges are reminders that were queued because the target agent
was asleep or was not at a safe interactive boundary yet.`,
	}
	cmd.AddCommand(
		newNudgeStatusCmd(stdout, stderr),
		newNudgeDrainCmd(stdout, stderr),
		newNudgePollCmd(stdout, stderr),
	)
	return cmd
}

func newNudgeStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status [agent]",
		Short: "Show queued and dead-letter nudges for an agent",
		Long: `Show queued and dead-letter nudges for an agent.

Defaults to $GC_AGENT when run inside an agent session.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdNudgeStatus(args, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
}

func newNudgeDrainCmd(stdout, stderr io.Writer) *cobra.Command {
	var inject bool
	cmd := &cobra.Command{
		Use:    "drain [agent]",
		Short:  "Deliver queued nudges for an agent",
		Long:   "Deliver queued nudges for an agent. Used by runtime hooks.",
		Args:   cobra.MaximumNArgs(1),
		Hidden: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdNudgeDrain(args, inject, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&inject, "inject", false, "emit <system-reminder> output for hook injection")
	return cmd
}

func newNudgePollCmd(stdout, stderr io.Writer) *cobra.Command {
	var sessionName string
	var interval time.Duration
	var quiescence time.Duration
	cmd := &cobra.Command{
		Use:    "poll [agent]",
		Short:  "Poll and deliver queued nudges for runtimes without turn hooks",
		Long:   "Poll and deliver queued nudges for runtimes without turn hooks. Used internally for Codex sessions.",
		Args:   cobra.MaximumNArgs(1),
		Hidden: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdNudgePoll(args, sessionName, interval, quiescence, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionName, "session", "", "runtime session name (defaults to $GC_SESSION_NAME)")
	cmd.Flags().DurationVar(&interval, "interval", defaultNudgePollInterval, "poll interval")
	cmd.Flags().DurationVar(&quiescence, "quiescence", defaultNudgePollQuiescence, "minimum inactivity before injecting")
	return cmd
}

func cmdNudgeStatus(args []string, stdout, stderr io.Writer) int {
	agentName := os.Getenv("GC_AGENT")
	if len(args) > 0 {
		agentName = args[0]
	}
	if agentName == "" {
		fmt.Fprintln(stderr, "gc nudge status: agent not specified (set $GC_AGENT or pass as argument)") //nolint:errcheck
		return 1
	}

	target, err := resolveNudgeTarget(agentName)
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge status: %v\n", err) //nolint:errcheck
		return 1
	}

	pending, dead, err := listQueuedNudges(target.cityPath, target.agent.QualifiedName(), time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge status: %v\n", err) //nolint:errcheck
		return 1
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "AGENT\tPENDING\tDEAD\tSESSION\n")                                                             //nolint:errcheck
	fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", target.agent.QualifiedName(), len(pending), len(dead), target.sessionName) //nolint:errcheck
	_ = tw.Flush()

	if len(pending) > 0 {
		fmt.Fprintln(stdout, "") //nolint:errcheck
		for _, item := range pending {
			_, _ = fmt.Fprintf(stdout, "pending  %s  due=%s  source=%s  %s\n",
				item.ID, formatDueTime(item.DeliverAfter), item.Source, item.Message)
		}
	}
	if len(dead) > 0 {
		fmt.Fprintln(stdout, "") //nolint:errcheck
		for _, item := range dead {
			_, _ = fmt.Fprintf(stdout, "dead     %s  reason=%s  source=%s  %s\n",
				item.ID, deadReason(item), item.Source, item.Message)
		}
	}
	return 0
}

func cmdNudgeDrain(args []string, inject bool, stdout, stderr io.Writer) int {
	agentName := os.Getenv("GC_AGENT")
	if len(args) > 0 {
		agentName = args[0]
	}
	if agentName == "" {
		if inject {
			return 0
		}
		fmt.Fprintln(stderr, "gc nudge drain: agent not specified (set $GC_AGENT or pass as argument)") //nolint:errcheck
		return 1
	}

	target, err := resolveNudgeTarget(agentName)
	if err != nil {
		if inject {
			return 0
		}
		fmt.Fprintf(stderr, "gc nudge drain: %v\n", err) //nolint:errcheck
		return 1
	}

	now := time.Now()
	items, err := dueQueuedNudges(target.cityPath, target.agent.QualifiedName(), now)
	if err != nil {
		if inject {
			return 0
		}
		fmt.Fprintf(stderr, "gc nudge drain: %v\n", err) //nolint:errcheck
		return 1
	}
	if len(items) == 0 {
		if inject {
			return 0
		}
		return 1
	}

	var out string
	if inject {
		out = formatNudgeInjectOutput(items)
	} else {
		out = formatNudgeRuntimeMessage(items)
	}
	if _, err := io.WriteString(stdout, out); err != nil {
		if inject {
			return 0
		}
		fmt.Fprintf(stderr, "gc nudge drain: writing output: %v\n", err) //nolint:errcheck
		return 1
	}
	if err := ackQueuedNudges(target.cityPath, queuedNudgeIDs(items)); err != nil && !inject {
		fmt.Fprintf(stderr, "gc nudge drain: %v\n", err) //nolint:errcheck
		return 1
	}
	return 0
}

func cmdNudgePoll(args []string, sessionName string, interval, quiescence time.Duration, _ io.Writer, stderr io.Writer) int {
	agentName := os.Getenv("GC_AGENT")
	if len(args) > 0 {
		agentName = args[0]
	}
	if agentName == "" {
		fmt.Fprintln(stderr, "gc nudge poll: agent not specified (set $GC_AGENT or pass as argument)") //nolint:errcheck
		return 1
	}
	target, err := resolveNudgeTarget(agentName)
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge poll: %v\n", err) //nolint:errcheck
		return 1
	}
	if sessionName != "" {
		target.sessionName = sessionName
	}
	if target.sessionName == "" {
		fmt.Fprintln(stderr, "gc nudge poll: session name unavailable") //nolint:errcheck
		return 1
	}

	release, err := acquireNudgePollerLease(target.cityPath, target.sessionName)
	if err != nil {
		if errors.Is(err, errNudgePollerRunning) {
			return 0
		}
		fmt.Fprintf(stderr, "gc nudge poll: %v\n", err) //nolint:errcheck
		return 1
	}
	defer release()

	sp := newSessionProvider()
	for {
		if !sp.IsRunning(target.sessionName) {
			return 0
		}
		delivered, pollErr := tryDeliverQueuedNudgesByPoller(target, sp, quiescence)
		if pollErr != nil {
			fmt.Fprintf(stderr, "gc nudge poll: %v\n", pollErr) //nolint:errcheck
		}
		if delivered {
			continue
		}
		time.Sleep(interval)
	}
}

func deliverSessionNudge(target nudgeTarget, message string, mode nudgeDeliveryMode, stdout, stderr io.Writer) int {
	return deliverSessionNudgeWithProvider(target, newSessionProvider(), message, mode, stdout, stderr)
}

func deliverSessionNudgeWithProvider(target nudgeTarget, sp runtime.Provider, message string, mode nudgeDeliveryMode, stdout, stderr io.Writer) int {
	switch mode {
	case nudgeDeliveryImmediate:
		if !sp.IsRunning(target.sessionName) {
			fmt.Fprintf(stderr, "gc session nudge: session %q is not running\n", target.agent.QualifiedName()) //nolint:errcheck
			return 1
		}
		if err := deliverImmediateNudge(sp, target.sessionName, runtime.TextContent(message)); err != nil {
			telemetry.RecordNudge(context.Background(), target.agent.QualifiedName(), err)
			fmt.Fprintf(stderr, "gc session nudge: %v\n", err) //nolint:errcheck
			return 1
		}
		telemetry.RecordNudge(context.Background(), target.agent.QualifiedName(), nil)
		fmt.Fprintf(stdout, "Nudged %s\n", target.agent.QualifiedName()) //nolint:errcheck
		return 0
	case nudgeDeliveryQueue:
		if err := enqueueQueuedNudge(target.cityPath, newQueuedNudge(target.agent.QualifiedName(), message, "session", time.Now())); err != nil {
			fmt.Fprintf(stderr, "gc session nudge: %v\n", err) //nolint:errcheck
			return 1
		}
		fmt.Fprintf(stdout, "Queued nudge for %s\n", target.agent.QualifiedName()) //nolint:errcheck
		return 0
	case nudgeDeliveryWaitIdle:
		if !sp.IsRunning(target.sessionName) {
			if err := enqueueQueuedNudge(target.cityPath, newQueuedNudge(target.agent.QualifiedName(), message, "session", time.Now())); err != nil {
				fmt.Fprintf(stderr, "gc session nudge: %v\n", err) //nolint:errcheck
				return 1
			}
			fmt.Fprintf(stdout, "Queued nudge for %s\n", target.agent.QualifiedName()) //nolint:errcheck
			return 0
		}
		if tryDeliverWaitIdleNudge(target, sp, message) {
			telemetry.RecordNudge(context.Background(), target.agent.QualifiedName(), nil)
			fmt.Fprintf(stdout, "Nudged %s\n", target.agent.QualifiedName()) //nolint:errcheck
			return 0
		}
		if err := enqueueQueuedNudge(target.cityPath, newQueuedNudge(target.agent.QualifiedName(), message, "session", time.Now())); err != nil {
			fmt.Fprintf(stderr, "gc session nudge: %v\n", err) //nolint:errcheck
			return 1
		}
		fmt.Fprintf(stdout, "Queued nudge for %s\n", target.agent.QualifiedName()) //nolint:errcheck
		return 0
	default:
		fmt.Fprintf(stderr, "gc session nudge: unknown delivery mode %q\n", mode) //nolint:errcheck
		return 1
	}
}

func sendMailNotify(target nudgeTarget, sender string) error {
	return sendMailNotifyWithProvider(target, newSessionProvider(), sender)
}

func sendMailNotifyWithProvider(target nudgeTarget, sp runtime.Provider, sender string) error {
	msg := fmt.Sprintf("You have mail from %s", sender)
	now := time.Now()
	if !sp.IsRunning(target.sessionName) || !tryDeliverWaitIdleNudge(target, sp, msg) {
		return enqueueQueuedNudge(target.cityPath, newQueuedNudge(target.agent.QualifiedName(), msg, "mail", now))
	}
	telemetry.RecordNudge(context.Background(), target.agent.QualifiedName(), nil)
	return nil
}

func resolveNudgeTarget(agentName string) (nudgeTarget, error) {
	cityPath, err := resolveCity()
	if err != nil {
		return nudgeTarget{}, err
	}
	cfg, err := loadCityConfig(cityPath)
	if err != nil {
		return nudgeTarget{}, err
	}
	found, ok := resolveAgentIdentity(cfg, agentName, currentRigContext(cfg))
	if !ok {
		return nudgeTarget{}, fmt.Errorf("agent %q not found in config", agentName)
	}
	resolved, err := config.ResolveProvider(&found, &cfg.Workspace, cfg.Providers, exec.LookPath)
	if err != nil {
		return nudgeTarget{}, err
	}
	cityName := cfg.Workspace.Name
	if cityName == "" {
		cityName = filepath.Base(cityPath)
	}
	sn := cliSessionName(cityPath, cityName, found.QualifiedName(), cfg.Workspace.SessionTemplate)
	return nudgeTarget{
		cityPath:    cityPath,
		cityName:    cityName,
		cfg:         cfg,
		agent:       found,
		resolved:    resolved,
		sessionName: sn,
	}, nil
}

func tryDeliverWaitIdleNudge(target nudgeTarget, sp runtime.Provider, message string) bool {
	if target.agent.Session == "acp" {
		err := sp.Nudge(target.sessionName, runtime.TextContent(message))
		return err == nil
	}
	if target.resolved == nil || target.resolved.Name != "claude" {
		return false
	}
	wp, ok := sp.(runtime.IdleWaitProvider)
	if !ok {
		return false
	}
	if err := wp.WaitForIdle(target.sessionName, defaultNudgeWaitIdleTimeout); err != nil {
		return false
	}
	if err := deliverImmediateNudge(sp, target.sessionName, runtime.TextContent(message)); err != nil {
		return false
	}
	return true
}

func deliverImmediateNudge(sp runtime.Provider, sessionName string, content []runtime.ContentBlock) error {
	if np, ok := sp.(runtime.ImmediateNudgeProvider); ok {
		return np.NudgeNow(sessionName, content)
	}
	return sp.Nudge(sessionName, content)
}

func parseNudgeDeliveryMode(raw string) (nudgeDeliveryMode, error) {
	switch nudgeDeliveryMode(raw) {
	case nudgeDeliveryImmediate, nudgeDeliveryWaitIdle, nudgeDeliveryQueue:
		return nudgeDeliveryMode(raw), nil
	default:
		return "", fmt.Errorf("unknown delivery mode %q (want immediate, wait-idle, or queue)", raw)
	}
}

func tryDeliverQueuedNudgesByPoller(target nudgeTarget, sp runtime.Provider, quiescence time.Duration) (bool, error) {
	items, err := dueQueuedNudges(target.cityPath, target.agent.QualifiedName(), time.Now())
	if err != nil || len(items) == 0 {
		return false, err
	}
	if !pollerSessionIdleEnough(sp, target.sessionName, quiescence) {
		return false, nil
	}
	msg := formatNudgeRuntimeMessage(items)
	if err := deliverImmediateNudge(sp, target.sessionName, runtime.TextContent(msg)); err != nil {
		telemetry.RecordNudge(context.Background(), target.agent.QualifiedName(), err)
		if recErr := recordQueuedNudgeFailure(target.cityPath, queuedNudgeIDs(items), err, time.Now()); recErr != nil {
			return false, recErr
		}
		return false, nil
	}
	telemetry.RecordNudge(context.Background(), target.agent.QualifiedName(), nil)
	return true, ackQueuedNudges(target.cityPath, queuedNudgeIDs(items))
}

func pollerSessionIdleEnough(sp runtime.Provider, sessionName string, quiescence time.Duration) bool {
	if !sp.Capabilities().CanReportActivity {
		return false
	}
	last, err := sp.GetLastActivity(sessionName)
	if err != nil || last.IsZero() {
		return false
	}
	return time.Since(last) >= quiescence
}

func maybeStartCodexNudgePoller(target nudgeTarget) {
	if target.resolved == nil || target.resolved.Name != "codex" {
		return
	}
	if target.sessionName == "" {
		return
	}
	if err := ensureNudgePoller(target.cityPath, target.agent.QualifiedName(), target.sessionName); err != nil {
		return
	}
}

func ensureNudgePoller(cityPath, agentName, sessionName string) error {
	pidPath := nudgePollerPIDPath(cityPath, sessionName)
	if running, _ := existingPollerPID(pidPath); running {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "nudge", "poll", "--city", cityPath, "--session", sessionName, agentName)
	cmd.Env = os.Environ()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func formatNudgeInjectOutput(items []queuedNudge) string {
	var sb strings.Builder
	sb.WriteString("<system-reminder>\n")
	if len(items) == 1 {
		sb.WriteString("You have a deferred reminder that was queued until a safe boundary:\n\n")
	} else {
		fmt.Fprintf(&sb, "You have %d deferred reminders that were queued until a safe boundary:\n\n", len(items))
	}
	for _, item := range items {
		fmt.Fprintf(&sb, "- [%s] %s\n", item.Source, item.Message)
	}
	sb.WriteString("\nHandle them after this turn.\n")
	sb.WriteString("</system-reminder>\n")
	return sb.String()
}

func formatNudgeRuntimeMessage(items []queuedNudge) string {
	var sb strings.Builder
	sb.WriteString("Deferred reminders:\n")
	for _, item := range items {
		fmt.Fprintf(&sb, "- [%s] %s\n", item.Source, item.Message)
	}
	sb.WriteString("\nThese were queued until the session went idle.\n")
	return sb.String()
}

func formatDueTime(ts time.Time) string {
	if ts.IsZero() {
		return "now"
	}
	d := time.Until(ts).Round(time.Second)
	switch {
	case d <= 0:
		return "now"
	case d < time.Minute:
		return d.String()
	default:
		return ts.Format(time.RFC3339)
	}
}

func deadReason(item queuedNudge) string {
	if item.LastError != "" {
		return item.LastError
	}
	if !item.ExpiresAt.IsZero() && item.ExpiresAt.Before(time.Now()) {
		return "expired"
	}
	return "dead-letter"
}

func newQueuedNudge(agentName, message, source string, now time.Time) queuedNudge {
	return queuedNudge{
		ID:           newQueuedNudgeID(),
		Agent:        agentName,
		Source:       source,
		Message:      message,
		CreatedAt:    now.UTC(),
		DeliverAfter: now.UTC(),
		ExpiresAt:    now.Add(defaultQueuedNudgeTTL).UTC(),
	}
}

func newQueuedNudgeID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("nudge-%d", time.Now().UnixNano())
	}
	return "nudge-" + hex.EncodeToString(buf[:])
}

func queuedNudgeIDs(items []queuedNudge) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func dueQueuedNudges(cityPath, agentName string, now time.Time) ([]queuedNudge, error) {
	var out []queuedNudge
	err := withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		pruneExpiredQueuedNudges(state, now)
		for _, item := range state.Pending {
			if item.Agent != agentName {
				continue
			}
			if !item.DeliverAfter.IsZero() && item.DeliverAfter.After(now) {
				continue
			}
			out = append(out, item)
		}
		return nil
	})
	return out, err
}

func listQueuedNudges(cityPath, agentName string, now time.Time) ([]queuedNudge, []queuedNudge, error) {
	var pending []queuedNudge
	var dead []queuedNudge
	err := withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		pruneExpiredQueuedNudges(state, now)
		for _, item := range state.Pending {
			if item.Agent == agentName {
				pending = append(pending, item)
			}
		}
		for _, item := range state.Dead {
			if item.Agent == agentName {
				dead = append(dead, item)
			}
		}
		return nil
	})
	return pending, dead, err
}

func enqueueQueuedNudge(cityPath string, item queuedNudge) error {
	return withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		pruneExpiredQueuedNudges(state, time.Now())
		state.Pending = append(state.Pending, item)
		sortQueuedNudges(state)
		return nil
	})
}

func ackQueuedNudges(cityPath string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	return withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		pruneExpiredQueuedNudges(state, time.Now())
		filtered := state.Pending[:0]
		for _, item := range state.Pending {
			if !want[item.ID] {
				filtered = append(filtered, item)
			}
		}
		state.Pending = filtered
		return nil
	})
}

func recordQueuedNudgeFailure(cityPath string, ids []string, cause error, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	return withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		pruneExpiredQueuedNudges(state, now)
		filtered := state.Pending[:0]
		for _, item := range state.Pending {
			if !want[item.ID] {
				filtered = append(filtered, item)
				continue
			}
			item.Attempts++
			item.LastAttemptAt = now.UTC()
			item.LastError = cause.Error()
			if item.Attempts >= defaultQueuedNudgeMaxAttempts || (!item.ExpiresAt.IsZero() && !item.ExpiresAt.After(now)) {
				item.DeadAt = now.UTC()
				state.Dead = append(state.Dead, item)
				continue
			}
			item.DeliverAfter = now.Add(defaultQueuedNudgeRetryDelay).UTC()
			filtered = append(filtered, item)
		}
		state.Pending = filtered
		sortQueuedNudges(state)
		return nil
	})
}

func pruneExpiredQueuedNudges(state *nudgeQueueState, now time.Time) {
	filtered := state.Pending[:0]
	for _, item := range state.Pending {
		if !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(now) {
			item.DeadAt = now.UTC()
			if item.LastError == "" {
				item.LastError = "expired"
			}
			state.Dead = append(state.Dead, item)
			continue
		}
		filtered = append(filtered, item)
	}
	state.Pending = filtered
	sortQueuedNudges(state)
}

func sortQueuedNudges(state *nudgeQueueState) {
	sort.SliceStable(state.Pending, func(i, j int) bool {
		if !state.Pending[i].DeliverAfter.Equal(state.Pending[j].DeliverAfter) {
			return state.Pending[i].DeliverAfter.Before(state.Pending[j].DeliverAfter)
		}
		if !state.Pending[i].CreatedAt.Equal(state.Pending[j].CreatedAt) {
			return state.Pending[i].CreatedAt.Before(state.Pending[j].CreatedAt)
		}
		return state.Pending[i].ID < state.Pending[j].ID
	})
	sort.SliceStable(state.Dead, func(i, j int) bool {
		if !state.Dead[i].DeadAt.Equal(state.Dead[j].DeadAt) {
			return state.Dead[i].DeadAt.Before(state.Dead[j].DeadAt)
		}
		if !state.Dead[i].CreatedAt.Equal(state.Dead[j].CreatedAt) {
			return state.Dead[i].CreatedAt.Before(state.Dead[j].CreatedAt)
		}
		return state.Dead[i].ID < state.Dead[j].ID
	})
}

func withNudgeQueueState(cityPath string, fn func(*nudgeQueueState) error) error {
	dir := filepath.Dir(nudgeQueueStatePath(cityPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating nudge queue dir: %w", err)
	}

	lockPath := nudgeQueueLockPath(cityPath)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening nudge queue lock: %w", err)
	}
	defer lockFile.Close() //nolint:errcheck

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking nudge queue: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	state, err := loadNudgeQueueState(cityPath)
	if err != nil {
		return err
	}
	if err := fn(&state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal nudge queue: %w", err)
	}
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, nudgeQueueStatePath(cityPath), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write nudge queue: %w", err)
	}
	return nil
}

func loadNudgeQueueState(cityPath string) (nudgeQueueState, error) {
	path := nudgeQueueStatePath(cityPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nudgeQueueState{}, nil
	}
	if err != nil {
		return nudgeQueueState{}, fmt.Errorf("read nudge queue: %w", err)
	}
	if len(data) == 0 {
		return nudgeQueueState{}, nil
	}
	var state nudgeQueueState
	if err := json.Unmarshal(data, &state); err != nil {
		return nudgeQueueState{}, fmt.Errorf("parse nudge queue: %w", err)
	}
	sortQueuedNudges(&state)
	return state, nil
}

func nudgeQueueStatePath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "nudges", "state.json")
}

func nudgeQueueLockPath(cityPath string) string {
	return citylayout.RuntimePath(cityPath, "nudges", "state.lock")
}

func nudgePollerPIDPath(cityPath, sessionName string) string {
	return citylayout.RuntimePath(cityPath, "nudges", "pollers", sessionName+".pid")
}

var errNudgePollerRunning = errors.New("nudge poller already running")

func acquireNudgePollerLease(cityPath, sessionName string) (func(), error) {
	pidPath := nudgePollerPIDPath(cityPath, sessionName)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating nudge poller dir: %w", err)
	}
	if running, _ := existingPollerPID(pidPath); running {
		return nil, errNudgePollerRunning
	}
	pid := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, pidPath, pid, 0o644); err != nil {
		return nil, fmt.Errorf("write nudge poller pid: %w", err)
	}
	return func() {
		current, err := os.ReadFile(pidPath)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(current)) == strings.TrimSpace(string(pid)) {
			_ = os.Remove(pidPath)
		}
	}, nil
}

func existingPollerPID(pidPath string) (bool, error) {
	data, err := os.ReadFile(pidPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	pidText := strings.TrimSpace(string(data))
	if pidText == "" {
		return false, nil
	}
	var pid int
	if _, err := fmt.Sscanf(pidText, "%d", &pid); err != nil || pid <= 0 {
		return false, nil
	}
	if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	return false, nil
}
