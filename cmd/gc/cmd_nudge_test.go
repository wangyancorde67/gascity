package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestDeliverSessionNudgeWithProviderWaitIdleQueuesForCodex(t *testing.T) {
	dir := t.TempDir()
	fake := runtime.NewFake()
	if err := fake.Start(context.Background(), "sess-worker", runtime.Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	target := nudgeTarget{
		cityPath:    dir,
		agent:       config.Agent{Name: "worker"},
		resolved:    &config.ResolvedProvider{Name: "codex"},
		sessionName: "sess-worker",
	}

	var stdout, stderr bytes.Buffer
	code := deliverSessionNudgeWithProvider(target, fake, "check deploy status", nudgeDeliveryWaitIdle, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("deliverSessionNudgeWithProvider = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Queued nudge for worker") {
		t.Fatalf("stdout = %q, want queued confirmation", stdout.String())
	}
	for _, call := range fake.Calls {
		if call.Method == "Nudge" {
			t.Fatalf("unexpected direct nudge call: %+v", call)
		}
	}

	pending, dead, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if len(dead) != 0 {
		t.Fatalf("dead = %d, want 0", len(dead))
	}
	if pending[0].Source != "session" {
		t.Fatalf("source = %q, want session", pending[0].Source)
	}
}

func TestSendMailNotifyWithProviderQueuesWhenSessionSleeping(t *testing.T) {
	dir := t.TempDir()
	target := nudgeTarget{
		cityPath:    dir,
		agent:       config.Agent{Name: "mayor"},
		resolved:    &config.ResolvedProvider{Name: "codex"},
		sessionName: "sess-mayor",
	}

	if err := sendMailNotifyWithProvider(target, runtime.NewFake(), "human"); err != nil {
		t.Fatalf("sendMailNotifyWithProvider: %v", err)
	}

	pending, dead, err := listQueuedNudges(dir, "mayor", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if len(dead) != 0 {
		t.Fatalf("dead = %d, want 0", len(dead))
	}
	if pending[0].Source != "mail" {
		t.Fatalf("source = %q, want mail", pending[0].Source)
	}
	if !strings.Contains(pending[0].Message, "You have mail from human") {
		t.Fatalf("message = %q, want mail reminder", pending[0].Message)
	}
}

func TestTryDeliverQueuedNudgesByPollerDeliversAndAcks(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Add(-1 * time.Minute)
	if err := enqueueQueuedNudge(dir, newQueuedNudge("worker", "review the deploy logs", "session", now)); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}

	fake := runtime.NewFake()
	if err := fake.Start(context.Background(), "sess-worker", runtime.Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	fake.Activity = map[string]time.Time{"sess-worker": time.Now().Add(-10 * time.Second)}

	target := nudgeTarget{
		cityPath:    dir,
		agent:       config.Agent{Name: "worker"},
		resolved:    &config.ResolvedProvider{Name: "codex"},
		sessionName: "sess-worker",
	}

	delivered, err := tryDeliverQueuedNudgesByPoller(target, fake, 3*time.Second)
	if err != nil {
		t.Fatalf("tryDeliverQueuedNudgesByPoller: %v", err)
	}
	if !delivered {
		t.Fatal("delivered = false, want true")
	}

	var nudgeCalls []runtime.Call
	for _, call := range fake.Calls {
		if call.Method == "Nudge" {
			nudgeCalls = append(nudgeCalls, call)
		}
	}
	if len(nudgeCalls) != 1 {
		t.Fatalf("nudge calls = %d, want 1", len(nudgeCalls))
	}
	if !strings.Contains(nudgeCalls[0].Message, "Deferred reminders:") {
		t.Fatalf("nudge message = %q, want deferred reminder wrapper", nudgeCalls[0].Message)
	}
	if !strings.Contains(nudgeCalls[0].Message, "review the deploy logs") {
		t.Fatalf("nudge message = %q, want original reminder", nudgeCalls[0].Message)
	}

	pending, dead, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(pending))
	}
	if len(dead) != 0 {
		t.Fatalf("dead = %d, want 0", len(dead))
	}
}

func TestQueuedNudgeFailureMovesToDeadLetter(t *testing.T) {
	dir := t.TempDir()
	item := newQueuedNudge("worker", "stuck reminder", "session", time.Now().Add(-time.Hour))
	if err := enqueueQueuedNudge(dir, item); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}

	for i := 0; i < defaultQueuedNudgeMaxAttempts; i++ {
		if err := recordQueuedNudgeFailure(dir, []string{item.ID}, context.DeadlineExceeded, time.Now().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("recordQueuedNudgeFailure(%d): %v", i, err)
		}
	}

	pending, dead, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(pending))
	}
	if len(dead) != 1 {
		t.Fatalf("dead = %d, want 1", len(dead))
	}
	if dead[0].Attempts != defaultQueuedNudgeMaxAttempts {
		t.Fatalf("attempts = %d, want %d", dead[0].Attempts, defaultQueuedNudgeMaxAttempts)
	}
}
