//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slackMock is an httptest.Server that stands in for the Slack Web API
// during E2E tests. It captures every chat.postMessage call (and a small
// set of related verbs) so tests can assert on what the slack-pack
// adapter actually sent across the wire, and it can synthesize inbound
// Slack events targeting an adapter callback URL supplied by the test.
//
// The mock deliberately implements the minimum surface needed to cover
// the slack-pack pipeline. It is not a fidelity reimplementation of the
// Slack Web API.
//
// CSRF gate: when requireGCRequestHeader is true, the mock returns 403
// on any /api/chat.* POST that lacks the X-GC-Request header. This pins
// the fix from gastownhall/gascity#1817 — the slack-pack adapter callback
// URL points at gc's own /svc/<service>/publish proxy, which gates
// private-service-proxy mutations on that header. A regression that
// drops the header silently fails delivery in production; this mock
// makes it fail the test.
type slackMock struct {
	t      *testing.T
	server *httptest.Server

	mu    sync.Mutex
	calls []slackCall

	tsCounter atomic.Int64

	// requireGCRequestHeader, when true, makes the mock return 403 on
	// /api/chat.* POSTs missing X-GC-Request: true. Off by default;
	// regression tests for #1817 turn it on.
	requireGCRequestHeader bool
}

// slackCall captures a single Slack Web API call as it arrived at the
// mock. text/thread_ts/blocks are pulled from the JSON body for the
// scenarios we need; less-used fields are kept in raw for tests that
// want to assert on specifics without growing the struct.
type slackCall struct {
	Method         string
	Channel        string
	ThreadTS       string
	Text           string
	Blocks         json.RawMessage
	IdempotencyKey string
	GCRequest      string
	Raw            json.RawMessage
	At             time.Time
}

func newSlackMock(t *testing.T) *slackMock {
	t.Helper()
	m := &slackMock{t: t}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.server.Close)
	return m
}

// URL returns the base URL of the mock — point the slack-pack adapter's
// outbound HTTP client at this when wiring the e2eCity.
func (m *slackMock) URL() string { return m.server.URL }

// requireGCRequest enables the CSRF gate for #1817 regression.
func (m *slackMock) requireGCRequest() { m.requireGCRequestHeader = true }

// Calls returns a snapshot of every captured Slack API call so far.
// Returned slice is safe for the caller to inspect without holding the
// mock's lock.
func (m *slackMock) Calls() []slackCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]slackCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// nextTS returns a deterministic message timestamp shaped like a real
// Slack ts (seconds.microseconds). Monotonically increasing per mock
// instance so ordering assertions are stable.
func (m *slackMock) nextTS() string {
	n := m.tsCounter.Add(1)
	return fmt.Sprintf("17000000%02d.0001%02d", n%100, n%100)
}

func (m *slackMock) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gcReq := r.Header.Get("X-GC-Request")
	if m.requireGCRequestHeader && gcReq != "true" {
		http.Error(w, "missing X-GC-Request: true", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var parsed map[string]json.RawMessage
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	call := slackCall{
		Method:    r.URL.Path,
		Raw:       json.RawMessage(body),
		GCRequest: gcReq,
		At:        time.Now(),
	}
	if v, ok := parsed["channel"]; ok {
		_ = json.Unmarshal(v, &call.Channel)
	}
	if v, ok := parsed["thread_ts"]; ok {
		_ = json.Unmarshal(v, &call.ThreadTS)
	}
	if v, ok := parsed["text"]; ok {
		_ = json.Unmarshal(v, &call.Text)
	}
	if v, ok := parsed["blocks"]; ok {
		call.Blocks = v
	}
	if v, ok := parsed["idempotency_key"]; ok {
		_ = json.Unmarshal(v, &call.IdempotencyKey)
	}

	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()

	ts := m.nextTS()
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"ok":      true,
		"ts":      ts,
		"channel": call.Channel,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// emitInboundEvent POSTs a synthetic Slack event payload at the adapter
// callback URL the caller provides — modeling the Slack → adapter leg
// that real production traffic flows over. The shape mirrors the
// `event_callback` envelope Slack sends.
func (m *slackMock) emitInboundEvent(t *testing.T, callbackURL, channel, user, text, ts, threadTS string) {
	t.Helper()
	event := map[string]any{
		"type":    "event_callback",
		"team_id": "T0TESTWS",
		"event": map[string]any{
			"type":      "message",
			"channel":   channel,
			"user":      user,
			"text":      text,
			"ts":        ts,
			"thread_ts": threadTS,
		},
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal inbound event: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build inbound request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("emit inbound event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("adapter rejected inbound event: status=%d body=%s", resp.StatusCode, respBody)
	}
}
