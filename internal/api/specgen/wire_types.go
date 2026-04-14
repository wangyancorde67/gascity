// Package specgen wire_types defines the WebSocket wire protocol envelope
// types for AsyncAPI spec generation. These mirror the internal types in
// websocket.go and websocket_subscription.go but are exported so the
// swaggest reflector can generate JSON Schema from them.
//
// These types ARE the protocol contract. If you change the wire format,
// update these types and regenerate the spec.
package specgen

import "encoding/json"

// WireRequestEnvelope is the client-to-server request message.
type WireRequestEnvelope struct {
	Type           string          `json:"type" description:"Must be 'request'" enum:"request"`
	ID             string          `json:"id" description:"Client-assigned correlation ID"`
	Action         string          `json:"action" description:"Dotted action name (e.g. 'beads.list')"`
	IdempotencyKey string          `json:"idempotency_key,omitempty" description:"Deduplication key for mutation replay"`
	Scope          *WireScope      `json:"scope,omitempty" description:"City targeting for supervisor connections"`
	Payload        json.RawMessage `json:"payload,omitempty" description:"Action-specific request payload"`
	Watch          *WireWatch      `json:"watch,omitempty" description:"Blocking query parameters"`
}

// WireScope targets a specific city on supervisor connections.
type WireScope struct {
	City string `json:"city,omitempty" description:"City name for supervisor-scoped requests"`
}

// WireWatch provides blocking query semantics.
type WireWatch struct {
	Index uint64 `json:"index" description:"Block until server index exceeds this value"`
	Wait  string `json:"wait,omitempty" description:"Maximum wait duration (e.g. '30s')"`
}

// WireResponseEnvelope is the server-to-client response for a successful action.
type WireResponseEnvelope struct {
	Type   string          `json:"type" description:"Must be 'response'" enum:"response"`
	ID     string          `json:"id" description:"Correlation ID matching the request"`
	Index  uint64          `json:"index,omitempty" description:"Server event index for watch semantics"`
	Result json.RawMessage `json:"result,omitempty" description:"Action-specific response payload"`
}

// WireHelloEnvelope is sent by the server immediately after WebSocket upgrade.
type WireHelloEnvelope struct {
	Type              string   `json:"type" description:"Must be 'hello'" enum:"hello"`
	Protocol          string   `json:"protocol" description:"Protocol version (e.g. 'gc.v1alpha1')"`
	ServerRole        string   `json:"server_role" description:"'city' or 'supervisor'" enum:"city,supervisor"`
	ReadOnly          bool     `json:"read_only" description:"True if mutations are disabled"`
	Capabilities      []string `json:"capabilities" description:"Sorted list of supported action names"`
	SubscriptionKinds []string `json:"subscription_kinds,omitempty" description:"Supported subscription types (e.g. 'events', 'session.stream')"`
}

// WireErrorEnvelope is sent by the server when a request fails.
type WireErrorEnvelope struct {
	Type    string           `json:"type" description:"Must be 'error'" enum:"error"`
	ID      string           `json:"id,omitempty" description:"Correlation ID (empty for connection-level errors)"`
	Code    string           `json:"code" description:"Machine-readable error code"`
	Message string           `json:"message" description:"Human-readable error message"`
	Details []WireFieldError `json:"details,omitempty" description:"Per-field validation errors"`
}

// WireFieldError is a per-field validation error.
type WireFieldError struct {
	Field   string `json:"field" description:"Field path (e.g. 'payload.name')"`
	Message string `json:"message" description:"What's wrong with this field"`
}

// WireEventEnvelope is sent by the server for subscription events.
type WireEventEnvelope struct {
	Type           string          `json:"type" description:"Must be 'event'" enum:"event"`
	SubscriptionID string          `json:"subscription_id" description:"Subscription that produced this event"`
	EventType      string          `json:"event_type" description:"Event type (e.g. 'bead.created')"`
	Index          uint64          `json:"index,omitempty" description:"Event sequence number"`
	Cursor         string          `json:"cursor,omitempty" description:"Resume cursor for reconnection"`
	Payload        json.RawMessage `json:"payload,omitempty" description:"Event-specific payload"`
}

// WireSubscriptionStartPayload is the payload for subscription.start.
type WireSubscriptionStartPayload struct {
	Kind        string `json:"kind" description:"Subscription type: 'events' or 'session.stream'"`
	AfterSeq    uint64 `json:"after_seq,omitempty" description:"Resume from this event sequence"`
	AfterCursor string `json:"after_cursor,omitempty" description:"Resume from this cursor"`
	Target      string `json:"target,omitempty" description:"Session ID or name (for session.stream)"`
	Format      string `json:"format,omitempty" description:"Stream format: 'text', 'raw', 'jsonl'"`
	Turns       int    `json:"turns,omitempty" description:"Most recent N turns (0=all)"`
}

// WireSubscriptionStopPayload is the payload for subscription.stop.
type WireSubscriptionStopPayload struct {
	SubscriptionID string `json:"subscription_id" description:"Subscription to stop"`
}
