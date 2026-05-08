//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSlackMock_CapturesPostMessage exercises the mock in isolation —
// no slack-pack adapter, no e2eCity. This pins the mock's contract so a
// later refactor that breaks capture semantics fails here, where the
// failure is unambiguous, instead of in the higher-level e2e tests
// where the failure could look like a slack-pack bug.
func TestSlackMock_CapturesPostMessage(t *testing.T) {
	t.Parallel()

	mock := newSlackMock(t)

	body := `{"channel":"C123","text":"hello","thread_ts":"1700000000.000100","idempotency_key":"k-1"}`
	req, err := http.NewRequest(http.MethodPost, mock.URL()+"/api/chat.postMessage", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ok, _ := decoded["ok"].(bool); !ok {
		t.Fatalf("expected ok=true in response, got %#v", decoded)
	}
	if ts, _ := decoded["ts"].(string); ts == "" {
		t.Fatalf("expected non-empty ts in response, got %#v", decoded)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 captured call, got %d", len(calls))
	}
	c := calls[0]
	if c.Channel != "C123" {
		t.Errorf("Channel = %q, want %q", c.Channel, "C123")
	}
	if c.Text != "hello" {
		t.Errorf("Text = %q, want %q", c.Text, "hello")
	}
	if c.ThreadTS != "1700000000.000100" {
		t.Errorf("ThreadTS = %q, want %q", c.ThreadTS, "1700000000.000100")
	}
	if c.IdempotencyKey != "k-1" {
		t.Errorf("IdempotencyKey = %q, want %q", c.IdempotencyKey, "k-1")
	}
	if c.GCRequest != "true" {
		t.Errorf("GCRequest = %q, want %q", c.GCRequest, "true")
	}
}

// TestSlackMock_CSRFGateRejectsMissingHeader pins the regression for
// gastownhall/gascity#1817 — when requireGCRequest() is enabled the
// mock returns 403 on /api/chat.* POSTs missing X-GC-Request: true.
// A regression that drops the header in HTTPAdapter.Publish would
// silently fail in production; this test makes that failure loud.
func TestSlackMock_CSRFGateRejectsMissingHeader(t *testing.T) {
	t.Parallel()

	mock := newSlackMock(t)
	mock.requireGCRequest()

	resp, err := http.DefaultClient.Post(
		mock.URL()+"/api/chat.postMessage",
		"application/json",
		strings.NewReader(`{"channel":"C1","text":"x"}`),
	)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without X-GC-Request, got %d", resp.StatusCode)
	}
	if calls := mock.Calls(); len(calls) != 0 {
		t.Errorf("CSRF-gated request should not have been recorded, got %d call(s)", len(calls))
	}
}

// TestSlackMock_EmitInboundEvent verifies that the mock can synthesize
// an inbound Slack event and POST it at a callback URL the test
// supplies. This is the Slack → adapter leg the higher-level e2e tests
// will use.
func TestSlackMock_EmitInboundEvent(t *testing.T) {
	t.Parallel()

	var (
		gotBody    []byte
		gotHeaders http.Header
		hits       atomic.Int32
	)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		gotBody = buf.Bytes()
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callback.Close)

	mock := newSlackMock(t)
	mock.emitInboundEvent(t, callback.URL, "C1", "U1", "hi", "1700000000.000200", "")

	if hits.Load() != 1 {
		t.Fatalf("expected callback to fire exactly once, got %d", hits.Load())
	}

	var env map[string]any
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody=%s", err, gotBody)
	}
	if env["type"] != "event_callback" {
		t.Errorf("envelope type = %v, want event_callback", env["type"])
	}
	ev, _ := env["event"].(map[string]any)
	if ev == nil {
		t.Fatalf("envelope had no event field; body=%s", gotBody)
	}
	if ev["channel"] != "C1" || ev["user"] != "U1" || ev["text"] != "hi" {
		t.Errorf("event payload mismatch: %#v", ev)
	}
	if got := gotHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}
