package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// trackingStore wraps a delegate beads.Store, recording which write
// operations land on it. Used to verify the tick-critical store path
// receives the writes inside reopenClosedConfiguredNamedSessionBead's
// lock window — reads still go to the underlying city store.
type trackingStore struct {
	beads.Store
	updates              []string
	setMetadataBatchKeys []string
	closes               []string
	updateErr            error
	setMetadataBatchErr  error
}

func (t *trackingStore) Update(id string, opts beads.UpdateOpts) error {
	t.updates = append(t.updates, id)
	if t.updateErr != nil {
		return t.updateErr
	}
	return t.Store.Update(id, opts)
}

func (t *trackingStore) SetMetadataBatch(id string, batch map[string]string) error {
	t.setMetadataBatchKeys = append(t.setMetadataBatchKeys, id)
	if t.setMetadataBatchErr != nil {
		return t.setMetadataBatchErr
	}
	return t.Store.SetMetadataBatch(id, batch)
}

func (t *trackingStore) Close(id string) error {
	t.closes = append(t.closes, id)
	return t.Store.Close(id)
}

// TestReopenLockWindow_TickCriticalStoreReceivesWrites verifies that
// reopenClosedConfiguredNamedSessionBead routes its lock-window writes
// (status reopen + metadata batch) to the tick-critical store when one
// is supplied. Reads remain on the city store. This is the main wiring
// invariant for ga-f4m2.1 — without it, the tighter subprocess timeout
// applies to nothing.
//
// Architecture: ga-f4m2.1.
func TestReopenLockWindow_TickCriticalStoreReceivesWrites(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NewMemStore()
	now := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{Name: "refinery", StartCommand: "true", MaxActiveSessions: intPtr(2)},
		},
		NamedSessions: []config.NamedSession{
			{Template: "refinery", Mode: "on_demand"},
		},
	}
	sessionName := config.NamedSessionRuntimeName(cfg.Workspace.Name, cfg.Workspace, "refinery")
	closed, err := store.Create(beads.Bead{
		Title:  "refinery",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":               sessionName,
			"alias":                      "refinery",
			"template":                   "refinery",
			"state":                      "suspended",
			"close_reason":               "suspended",
			namedSessionMetadataKey:      "true",
			namedSessionIdentityMetadata: "refinery",
			namedSessionModeMetadata:     "on_demand",
		},
	})
	if err != nil {
		t.Fatalf("create closed canonical bead: %v", err)
	}
	if err := store.Close(closed.ID); err != nil {
		t.Fatalf("close canonical bead: %v", err)
	}

	tickStore := &trackingStore{Store: store}

	var stderr bytes.Buffer
	reopened, ok := reopenClosedConfiguredNamedSessionBead(
		cityPath, store, tickStore, cfg, "test-city", "refinery", sessionName, "active", now, nil, &stderr,
	)
	if !ok {
		t.Fatalf("reopenClosedConfiguredNamedSessionBead failed: %s", stderr.String())
	}
	if reopened.ID != closed.ID {
		t.Fatalf("reopened.ID = %q, want %q", reopened.ID, closed.ID)
	}
	if len(tickStore.updates) != 1 || tickStore.updates[0] != closed.ID {
		t.Fatalf("tickStore.updates = %v, want [%s]", tickStore.updates, closed.ID)
	}
	if len(tickStore.setMetadataBatchKeys) != 1 || tickStore.setMetadataBatchKeys[0] != closed.ID {
		t.Fatalf("tickStore.setMetadataBatchKeys = %v, want [%s]", tickStore.setMetadataBatchKeys, closed.ID)
	}
}

// TestReopenLockWindow_TickCriticalTimeout verifies that when the
// tick-critical store's Update returns a timeout-style error, the
// reopen function returns gracefully (no hang, no panic) and the
// session-identifier lock is released. The acceptance gate is that
// the function returns within a small multiple of the simulated
// budget — proving the lock window cannot grow unbounded under bd
// subprocess timeouts.
//
// Architecture: ga-f4m2.1.
func TestReopenLockWindow_TickCriticalTimeout(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NewMemStore()
	now := time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Agents: []config.Agent{
			{Name: "refinery", StartCommand: "true", MaxActiveSessions: intPtr(2)},
		},
		NamedSessions: []config.NamedSession{
			{Template: "refinery", Mode: "on_demand"},
		},
	}
	sessionName := config.NamedSessionRuntimeName(cfg.Workspace.Name, cfg.Workspace, "refinery")
	closed, err := store.Create(beads.Bead{
		Title:  "refinery",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":               sessionName,
			"alias":                      "refinery",
			"template":                   "refinery",
			"state":                      "suspended",
			"close_reason":               "suspended",
			namedSessionMetadataKey:      "true",
			namedSessionIdentityMetadata: "refinery",
			namedSessionModeMetadata:     "on_demand",
		},
	})
	if err != nil {
		t.Fatalf("create closed canonical bead: %v", err)
	}
	if err := store.Close(closed.ID); err != nil {
		t.Fatalf("close canonical bead: %v", err)
	}

	tickStore := &trackingStore{
		Store:     store,
		updateErr: errors.New("timed out after 30s: context deadline exceeded"),
	}

	var stderr bytes.Buffer
	start := time.Now()
	_, ok := reopenClosedConfiguredNamedSessionBead(
		cityPath, store, tickStore, cfg, "test-city", "refinery", sessionName, "active", now, nil, &stderr,
	)
	elapsed := time.Since(start)

	if ok {
		t.Fatalf("reopenClosedConfiguredNamedSessionBead returned ok=true, want false on tick-critical timeout")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("elapsed = %s, want fast return on tick-critical timeout (lock window must not block)", elapsed)
	}
	if len(tickStore.updates) != 1 {
		t.Fatalf("tickStore.updates = %v, want exactly 1 attempt", tickStore.updates)
	}
	if len(tickStore.setMetadataBatchKeys) != 0 {
		t.Fatalf("tickStore.setMetadataBatchKeys = %v, want 0 — Update failure must short-circuit before setMetaBatch", tickStore.setMetadataBatchKeys)
	}
	if !strings.Contains(stderr.String(), "reopening configured named session") {
		t.Fatalf("stderr missing reopen failure log: %q", stderr.String())
	}
}

// TestCloseBead_TickCriticalStoreReceivesWrites verifies the low-level
// close helper writes its metadata patch and Close operation through the
// supplied tick-critical store. Ownership checks remain outside closeBead.
//
// Architecture: ga-f4m2.1.
func TestCloseBead_TickCriticalStoreReceivesWrites(t *testing.T) {
	store := beads.NewMemStore()
	sessionBead, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "worker-1",
			"state":        "active",
		},
	})
	if err != nil {
		t.Fatalf("create session bead: %v", err)
	}

	tickStore := &trackingStore{Store: store}
	var stderr bytes.Buffer
	now := time.Date(2026, 5, 1, 9, 45, 0, 0, time.UTC)
	if !closeBead(store, tickStore, sessionBead.ID, "stale-session", now, &stderr) {
		t.Fatalf("closeBead returned false: %s", stderr.String())
	}
	if len(tickStore.setMetadataBatchKeys) != 1 || tickStore.setMetadataBatchKeys[0] != sessionBead.ID {
		t.Fatalf("tickStore.setMetadataBatchKeys = %v, want [%s]", tickStore.setMetadataBatchKeys, sessionBead.ID)
	}
	if len(tickStore.closes) != 1 || tickStore.closes[0] != sessionBead.ID {
		t.Fatalf("tickStore.closes = %v, want [%s]", tickStore.closes, sessionBead.ID)
	}
	got, err := store.Get(sessionBead.ID)
	if err != nil {
		t.Fatalf("get session bead: %v", err)
	}
	if got.Status != "closed" {
		t.Fatalf("status = %q, want closed", got.Status)
	}
}
