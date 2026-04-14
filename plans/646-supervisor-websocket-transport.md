# Implement #646: Supervisor WebSocket Transport

## Summary

Issue #646 replaces the HTTP/SSE API surface with WebSocket as the primary transport. This is a public API that external third-party clients build against.

### Transport exclusivity

Every endpoint is **WS-only** or **HTTP-only**, never both. When migration is complete:

- **~95% of endpoints move to WS-only** via `GET /v0/ws`
- **~5% remain HTTP-only** (justified operational endpoints)
- **Zero SSE endpoints** — all streaming moves to WS subscriptions
- **Zero duplicate surfaces** — no endpoint exists on both transports

### HTTP survivors (justified)

| Endpoint                     | Justification                                                           |
| ---------------------------- | ----------------------------------------------------------------------- |
| `GET /health`                | K8s/LB probe — probes can't do WS upgrade                               |
| `GET /v0/readiness`          | Operational probe                                                       |
| `GET /v0/provider-readiness` | Operational probe                                                       |
| `POST /v0/city`              | Process manager registration, not client API                            |
| `/svc/*`                     | TCP/HTTP proxy passthrough to workspace services                        |
| `/debug/pprof/*`             | Go runtime profiling, developer tool                                    |
| `GET /v0/ws`                 | WS upgrade endpoint (inherently HTTP)                                   |
| `GET /v0/asyncapi.yaml`      | Auto-generated spec discovery — clients need contract before WS connect |
| `GET /v0/openapi.yaml`       | Auto-generated spec discovery — clients need contract before WS connect |

### Architecture

- CLI and other Go clients use a persistent WebSocket client with auto-reconnect and exponential backoff. No HTTP fallback.
- The browser connects directly to the supervisor's `/v0/ws` endpoint. No SSE proxy.
- The dashboard server is reduced to serving static files only — zero API endpoints. All data fetching, mutations, and streaming go directly from browser to supervisor via WS.
- External third-party clients connect via WS and use the same protocol.

### Protocol contract

- AsyncAPI (WS) and OpenAPI (HTTP) specs auto-generated from annotated Go types as a single source of truth
- Specs served at well-known endpoints for client discovery
- Go types are the source of truth; specs are derived, not hand-written

## Implementation Guardrails

The implementation should follow these constraints throughout all phases:

- TDD first: add or update failing protocol/client parity tests before each migrated transport slice, then implement until those tests pass
- Layered architecture: keep transport code at the edge, a typed application/execution layer in the middle, and existing domain logic below it
- Serialization only at the edges: decode WebSocket/HTTP payloads into typed DTOs at the boundary, operate on typed values internally, and encode only on the way out
- DRY and SRP: extract shared request execution, event fan-out, scope resolution, and error mapping instead of duplicating them across HTTP and WebSocket handlers
- KISS and YAGNI: do not add speculative chunking, browser rewrites, new auth models, or transport-specific abstractions that are not needed for current parity
- Async notifications over polling: use subscriptions for ongoing state changes; retain one-shot watch semantics only where needed to match existing `index` + `wait` behavior
- Stateless reconnect semantics: rely on cursors, idempotency keys, and explicit subscription state instead of sticky server affinity or hidden per-client state
- No swallowed errors: invalid envelopes, size-limit violations, keepalive failures, scope mismatches, and reconnect/resume failures must produce structured errors or close codes and be logged centrally
- Observability by default: add connection/request/subscription logs, metrics, and trace points for handshake, dispatch, subscription lifecycle, reconnect, close reasons, and backpressure/drop conditions
- Maintainability over cleverness: prefer small typed helpers and incremental extraction over large framework-style rewrites

## Target Architecture

### WebSocket endpoint placement

Expose the same WebSocket protocol on both API server types:

- per-city server: `GET /v0/ws`
- supervisor mux: `GET /v0/ws`

The protocol is shared, but scope handling differs:

- per-city server: city scope is implicit
- supervisor mux: city scope is carried in the message envelope for city-targeted operations

This preserves current client behavior, because today callers can hit either:

- a standalone/city-local API server
- the supervisor API with city-scoped routing

### Dashboard architecture

The dashboard browser connects directly to the supervisor’s `/v0/ws` endpoint. The dashboard server is reduced to a static file server with zero API endpoints.

- Browser opens WS connection to supervisor and uses the same protocol as Go clients
- All 17 existing `/api/*` dashboard proxy endpoints are eliminated
- `/api/run` (subprocess command execution) is eliminated — browser calls supervisor WS actions directly
- Server-side HTML template rendering is eliminated — browser renders from JSON
- SSE (`EventSource`) is eliminated — browser uses WS subscriptions
- The dashboard server serves only static files (JS, CSS, HTML)

### Protocol framing

Use JSON envelopes with explicit request/response/event typing.

Client request envelope:

- `type: "request"`
- `id`
- `action`
- optional `idempotency_key` for create/retry-safe operations that currently rely on `Idempotency-Key`
- `scope` (optional; includes `city` where needed)
- `payload`

Server response envelope:

- `type: "response"`
- `id`
- optional `index` carrying the current event sequence for parity with `X-GC-Index`
- `result`

Server error envelope:

- `type: "error"`
- `id`
- `code`
- `message`
- optional typed details

Server event envelope:

- `type: "event"`
- `subscription_id`
- `event_type`
- optional cursor/resume token
- payload

Server hello envelope:

- `type: "hello"`
- protocol version
- server role (`city` or `supervisor`)
- read-only / mutation capability
- supported actions and subscription kinds

### Connection and concurrency model

The protocol should assume concurrent in-flight requests on a single socket:

- clients may send multiple requests without waiting for prior responses
- the server may process requests concurrently
- responses are correlated by `id` and may arrive out of request order
- subscription events may interleave with responses
- ordering is guaranteed only within a single subscription stream as defined by its cursor semantics

Implementation guidance:

- keep a single serialized writer per connection
- make dispatcher/request handlers safe for concurrent execution
- do not rely on request/response ordering for correctness

### Keepalive and liveness

Replace SSE keepalive comments with native WebSocket liveness:

- server sends periodic ping frames
- clients must respond with pong frames
- idle/dead connections are closed proactively
- reconnecting clients use normal cursor/resume mechanisms where supported

### Subscription model

Use explicit subscribe/unsubscribe requests over the socket.

Initial subscription families should match existing streaming surfaces and blocking-watch semantics:

- global events feed
- city-scoped events feed
- session stream
- one-shot blocking query equivalents for existing `index` + `wait` patterns

There is no distinct WebSocket "agent output stream" subscription kind in v1. The canonical streaming surface is session-scoped. If legacy HTTP compatibility keeps an agent-output stream alias during coexistence, it should map internally onto the session stream model rather than introducing a second protocol concept.

Blocking HTTP reads such as `?index=...&wait=...` should map to one of:

- a request option like `watch: {index, wait}` for one-shot “wait until changed” semantics
- or a short-lived subscription with a clear completion condition

Do not lose the current cursor/reconnect behavior:

- SSE `Last-Event-ID` / `after_seq`
- supervisor global composite cursor behavior

Session stream subscriptions need explicit parameters and completion rules:

- support the current session stream format modes rather than assuming one generic shape
- closed sessions emit a bounded snapshot/terminal sequence and then complete instead of remaining live forever
- live sessions remain open and continue streaming updates with normal cursor semantics
- `turns: N` returns the most recent N turns (0=all, 1=latest turn, 5=latest 5 turns). Used consistently for both `session.transcript` (one-shot fetch) and `subscription.start kind=session.stream` (live streaming with initial snapshot). Replaces HTTP `?tail=N`.

## Migration Scope

### Client-facing API domains that must be accounted for

The plan must treat the supervisor client surface as the current full set of client-used domains, not a narrow subset. At minimum, the migration inventory needs to cover:

- supervisor/global: cities, readiness, provider readiness, health, global events
- city status/config: status, config, config explain/validate
- agents: list/get, actions, output surfaces
- rigs: list/get, CRUD/actions where client-facing
- sessions: list/get, transcript, pending, stream, messages/submit/respond/wake/kill/close/rename/agents
- beads: list/get/graph/ready/update/assign/close/reopen/delete/create
- mail
- convoys
- orders / formulas / workflow aliases
- providers and provider CRUD
- patches
- services (status/restart only; keep `/svc/` proxy on HTTP)
- sling
- packs
- extmsg
- events list/emit/stream

The implementation can migrate these in phases, but the plan must inventory them up front so “full cutover” has a concrete meaning.

### Out of scope for #646

- new remote mutation auth model for non-localhost supervisor access

### Route disposition matrix

Every former HTTP route is classified as **WS-only** (migrated), **HTTP-only** (justified survivor), or **Removed** (dead).

#### HTTP-only survivors (8 routes)

| Route                        | Justification                                                           |
| ---------------------------- | ----------------------------------------------------------------------- |
| `GET /v0/ws`                 | WS upgrade endpoint (inherently HTTP)                                   |
| `GET /health`                | K8s/LB probe — probes can't do WS upgrade                               |
| `GET /v0/readiness`          | Operational probe                                                       |
| `GET /v0/provider-readiness` | Operational probe                                                       |
| `POST /v0/city`              | Process manager registration, not client API                            |
| `/svc/*`                     | TCP/HTTP proxy passthrough to workspace services                        |
| `GET /v0/asyncapi.yaml`      | Auto-generated spec discovery — clients need contract before WS connect |
| `GET /v0/openapi.yaml`       | Auto-generated spec discovery — clients need contract before WS connect |

#### WS-only actions (119 actions)

| Domain        | WS Actions                                                                                                                                                                                                                                                                                                                                 |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Health/Status | `health.get`, `status.get`                                                                                                                                                                                                                                                                                                                 |
| City/Config   | `city.get`, `city.patch`, `config.get`, `config.explain`, `config.validate`, `cities.list`                                                                                                                                                                                                                                                 |
| Agents        | `agents.list`, `agent.get`, `agent.create`, `agent.update`, `agent.delete`, `agent.suspend`, `agent.resume`                                                                                                                                                                                                                                |
| Rigs          | `rigs.list`, `rig.get`, `rig.create`, `rig.update`, `rig.delete`, `rig.suspend`, `rig.resume`, `rig.restart`                                                                                                                                                                                                                               |
| Providers     | `providers.list`, `provider.get`, `provider.create`, `provider.update`, `provider.delete`                                                                                                                                                                                                                                                  |
| Beads         | `beads.list`, `beads.ready`, `beads.graph`, `bead.get`, `bead.deps`, `bead.create`, `bead.close`, `bead.update`, `bead.reopen`, `bead.assign`, `bead.delete`                                                                                                                                                                               |
| Mail          | `mail.list`, `mail.get`, `mail.count`, `mail.thread`, `mail.read`, `mail.mark_unread`, `mail.archive`, `mail.reply`, `mail.send`, `mail.delete`                                                                                                                                                                                            |
| Sessions      | `sessions.list`, `session.get`, `session.create`, `session.patch`, `session.submit`, `session.messages`, `session.stop`, `session.kill`, `session.suspend`, `session.close`, `session.wake`, `session.rename`, `session.respond`, `session.pending`, `session.transcript`, `session.agents.list`, `session.agent.get`                      |
| Convoys       | `convoys.list`, `convoy.get`, `convoy.create`, `convoy.add`, `convoy.remove`, `convoy.check`, `convoy.close`, `convoy.delete`                                                                                                                                                                                                              |
| Events        | `events.list`, `event.emit`                                                                                                                                                                                                                                                                                                                |
| Orders        | `orders.list`, `orders.check`, `orders.feed`, `orders.history`, `order.get`, `order.enable`, `order.disable`, `order.history.detail`                                                                                                                                                                                                       |
| Formulas      | `formulas.list`, `formulas.feed`, `formula.get`, `formula.runs`                                                                                                                                                                                                                                                                            |
| Workflows     | `workflow.get`, `workflow.delete`                                                                                                                                                                                                                                                                                                          |
| Sling         | `sling.run`                                                                                                                                                                                                                                                                                                                                |
| Services      | `services.list`, `service.get`, `service.restart`                                                                                                                                                                                                                                                                                          |
| Packs         | `packs.list`                                                                                                                                                                                                                                                                                                                               |
| Patches       | `patches.agents.list`, `patches.agent.get`, `patches.agents.set`, `patches.agent.delete`, `patches.rigs.list`, `patches.rig.get`, `patches.rigs.set`, `patches.rig.delete`, `patches.providers.list`, `patches.provider.get`, `patches.providers.set`, `patches.provider.delete`                                                           |
| ExtMsg        | `extmsg.inbound`, `extmsg.outbound`, `extmsg.bindings.list`, `extmsg.bind`, `extmsg.unbind`, `extmsg.groups.lookup`, `extmsg.groups.ensure`, `extmsg.participant.upsert`, `extmsg.participant.remove`, `extmsg.transcript.list`, `extmsg.transcript.ack`, `extmsg.adapters.list`, `extmsg.adapters.register`, `extmsg.adapters.unregister` |
| Subscriptions | `subscription.start` (events, session.stream), `subscription.stop`                                                                                                                                                                                                                                                                         |

#### Removed (SSE endpoints)

| Former Route                  | Replacement                                   |
| ----------------------------- | --------------------------------------------- |
| `GET /v0/events/stream`       | `subscription.start {kind: "events"}`         |
| `GET /v0/session/{id}/stream` | `subscription.start {kind: "session.stream"}` |

## Implementation Phases

### Phase 1: Protocol foundation and shared execution layer

- Write failing protocol tests first for handshake, correlation, close/error behavior, and the first migrated request/response actions
- Add AsyncAPI spec for the WebSocket protocol using swaggest/go-asyncapi for spec generation from Go types
- Introduce the transport-neutral execution layer incrementally, not as a full up-front rewrite of all HTTP handlers
  - start with shared query/command functions for the first migrated domains
  - continue extracting typed inputs/outputs and shared error mapping as each domain moves
  - avoid duplicating business logic between HTTP and WebSocket, but do not block phase 1 on extracting the entire API surface at once
- Add `GET /v0/ws` to both `internal/api.Server` and `internal/api.SupervisorMux`
- Implement handshake, request dispatch, error envelopes, and subscription lifecycle
- Preserve current read-only semantics in the WebSocket layer
- Keep HTTP/SSE endpoints live during this phase

### Phase 2: Streaming parity

- Write failing parity tests first for each migrated SSE/blocking surface
- Migrate existing SSE/event surfaces to WebSocket subscriptions:
  - per-city events
  - supervisor global events
  - session stream
- Migrate blocking query semantics that depend on `X-GC-Index`, `index`, and `wait`
- Add reconnect/cursor parity tests for event and session flows
- Keep old SSE endpoints live until all internal clients have switched

### Phase 3: Go client migration

- Write failing client parity tests first for all migrated `internal/api.Client` methods
- Replace `internal/api.Client` HTTP transport with a persistent WebSocket client with auto-reconnect and exponential backoff (1s, 2s, 4s, 8s, 16s, 30s max)
- **No HTTP fallback** — every method goes through WS or returns an error
- Add subscription API to Go client: `SubscribeEvents`, `SubscribeSessionStream`, `Unsubscribe`
- Add `Close()` method for clean connection shutdown
- Preserve current routing behavior:
  - standalone city-local client path
  - supervisor client path
  - implicit single-running-city behavior where it exists today
  - explicit `city_required` errors where it exists today
- Define supervisor-vs-city scoping behavior explicitly:
  - on supervisor sockets, `scope.city` is required whenever the current HTTP surface would require an explicit city
  - on per-city sockets, omitted `scope.city` means the implicit city
  - on per-city sockets, an explicit matching `scope.city` is accepted
  - on per-city sockets, a different `scope.city` is a validation error

### Phase 4: Dashboard rewrite to static SPA

- Rewrite `dashboard.js` as a WS-native SPA that connects directly to supervisor `/v0/ws`
- Replace all `/api/*` fetch calls with WS actions
- Replace `EventSource` SSE with WS subscriptions
- Render all data client-side from JSON (eliminate server-side HTML templates)
- Add WS reconnect with exponential backoff in browser JS
- Strip the dashboard server to static file serving only — zero API endpoints
- Eliminate `/api/run` — browser calls supervisor WS actions directly
- Eliminate all 17 `/api/*` proxy endpoints
- Eliminate server-rendered CSRF tokens — browser uses Origin-based WS auth

### Phase 5: Remove legacy HTTP/SSE endpoints

- Remove all HTTP route registrations except the justified HTTP survivors (see table in Summary)
- Remove all SSE streaming endpoints (`/v0/events/stream`, session stream, agent output stream, etc.)
- Remove HTTP handler functions that are now WS-only
- Remove HTTP fallback paths from Go client (`doGet`, `doMutation`, `socketGetAction`, `socketPostAction`)
- Remove `httpClient` usage from `internal/api.Client` for API calls
- Verify zero SSE endpoints remain in codebase
- Verify zero duplicate HTTP+WS endpoints

## Security and Access Model

- Preserve current localhost/private-bind mutation semantics
- On WebSocket upgrade, validate `Origin` against the same localhost/private-host policy that protects the current browser-facing API surface
- On WebSocket connect, advertise read-only capability in `hello`
- Reject mutating actions when the server is in read-only mode
- The current HTTP `X-GC-Request` CSRF mechanism does not apply to WebSocket frames; mutation authorization is established at handshake time and enforced for the lifetime of the connection
- Browser-direct WS connections use the same Origin validation as Go clients — localhost enforcement at handshake time
- Server-rendered CSRF tokens (`<meta name="dashboard-token">`) are eliminated along with the dashboard server API
- The dashboard server no longer mediates browser access — the browser connects directly to the supervisor/city WS endpoint

## Operational Semantics

### City lifecycle under supervisor subscriptions

When a supervisor-scoped subscription targets a specific city and that city stops, restarts, or disappears:

- emit a terminal subscription event/error indicating the target city became unavailable
- end that subscription cleanly
- require the client to resubscribe after the city is available again

This matches current resolver-style behavior more closely than trying to make subscriptions survive arbitrary city process churn.

### WebSocket close codes

Use explicit close codes so clients can distinguish expected shutdown from policy errors:

- `1000` for normal close
- `1001` for server shutdown/restart or supervisor lifecycle transitions
- `1008` for policy violations such as invalid origin or forbidden mutation attempts
- `1011` for internal server errors where the connection cannot continue safely

### Message size limits

Set explicit message size limits rather than leaving large payload behavior implicit:

- enforce a bounded maximum inbound message size
- define and test bounded outbound behavior so oversized responses fail explicitly rather than hanging or truncating silently
- keep large transcript/content reads on typed request/response operations rather than inventing ad hoc chunking in phase 1
- if a response class proves too large for a single message in practice, add explicit chunked protocol support as a later protocol revision rather than silently truncating

### Observability and diagnostics

The WebSocket transport should emit enough telemetry to debug production failures without packet-level forensics:

- connection lifecycle logs with remote address, server role, origin decision, and close code
- request logs/traces keyed by request `id`, action, scope, latency, and outcome
- subscription lifecycle logs/traces keyed by subscription id, kind, scope, cursor, and termination reason
- metrics for active connections, active subscriptions, request latency, error counts, ping/pong failures, reconnect attempts, and oversize message rejections
- explicit logging for fallback-to-direct-mutation paths so transport failures are visible rather than silently masked

## Test Plan

### Protocol tests

- handshake on city server and supervisor mux
- request/response correlation
- structured error mapping
- malformed envelope rejection
- read-only mutation rejection
- city scope required vs auto-resolved behavior
- request/response concurrency and out-of-order response correlation
- ping/pong keepalive behavior and dead-connection detection
- idempotency-key replay protection for create operations
- close-code behavior for normal shutdown, policy violation, and internal error cases

### Subscription tests

- per-city event subscription
- global event subscription with composite cursor parity
- session stream parity
- unsubscribe behavior
- reconnect/resume behavior
- city-unavailable termination behavior on supervisor-scoped city subscriptions

### Client migration tests

- `internal/api.Client` parity tests for all WS-migrated methods
- CLI command tests covering WS-only API routing paths
- client reconnect with backoff tests
- client subscription API tests (events, session stream)

### Migration safety tests

- blocking query (watch) behavior preserves semantics over WS
- standalone city-local server and supervisor mux both serve the same WS protocol correctly
- service proxy routes remain HTTP and unaffected
- transport failure paths are observable (no silent masking)

## Assumptions and Defaults

- All 5 phases complete in this branch before merge
- The dashboard browser connects directly to the supervisor WebSocket endpoint
- The dashboard server is a static file server only — zero API endpoints
- WebSocket is the exclusive client transport; HTTP remains only for justified operational endpoints
- AsyncAPI + OpenAPI specs are auto-generated from annotated Go types (single source of truth)
- The Go client auto-reconnects with exponential backoff; no HTTP fallback
- Browser auth uses Origin-based WS validation (localhost enforcement); server-rendered CSRF tokens are eliminated
- Fern is out of scope
- `.env` is already handled elsewhere and is not part of this plan
