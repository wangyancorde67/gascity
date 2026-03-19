# Dashboard

The dashboard is a web UI compiled into the `gc` binary for monitoring
convoys, agents, mail, rigs, sessions, and events in real time.

## Prerequisites

The dashboard is a separate web server. It needs a GC API server to talk to,
and `gc dashboard serve` still must be run from inside a city directory so it
can resolve local command context.

### Standalone city mode

If you are using `gc start` without the machine-wide supervisor, the dashboard
talks to that city's own API server. Ensure the city API is enabled in
`city.toml`:

```toml
[api]
port = 9443
```

Then start the city normally with `gc start`. The API server starts with the
controller on that port.

### Supervisor mode

If you are using the machine-wide supervisor, the dashboard talks to the
supervisor API instead. The default supervisor API address is:

```text
http://127.0.0.1:8372
```

In this mode, per-city `[api]` ports are ignored. The dashboard detects
supervisor mode automatically via `/v0/cities`, enables a city selector, and
routes requests through `/v0/city/{name}/...`.

## Starting the dashboard

```
gc dashboard serve --api http://127.0.0.1:9443  # Standalone city API
gc dashboard serve --api http://127.0.0.1:8372  # Supervisor API
gc dashboard serve --port 3000 --api http://127.0.0.1:8372
```

The `--api` flag is required. It points to the GC API server URL
(either the standalone city API or the supervisor API).

## Features

The dashboard provides:

- **Convoys** — progress tracking, tracked issues, create new convoys
- **Crew** — named worker status with activity detection
- **Polecats** — ephemeral worker activity and work status
- **Activity timeline** — categorized event feed with filters
- **Mail** — inbox with threading, compose, and all-traffic view
- **Merge queue** — open PRs with CI and mergeable status
- **Escalations** — priority-colored escalation list
- **Ready work** — items available for assignment
- **Health** — system heartbeat and agent counts
- **Issues** — backlog with priority, age, labels, assignment
- **Command palette** (Cmd+K) — execute gc commands from the browser

Real-time updates via SSE (Server-Sent Events) from the API server.
