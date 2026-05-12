# Gas City SDK Roadmap

This file tracks SDK-level work still needed for the Gastown example. It is
the strategic companion to `FUTURE.md`: `FUTURE.md` is the concise current gap
inventory; this file explains what should or should not become Go code.

Most of the original roadmap has shipped. Do not use old `gt` command parity
as the standard for new SDK work. Gas City should only grow Go surfaces for
session/provider boundaries, controller-owned lifecycle, atomic workflow
operations, typed API/event surfaces, and reusable configuration mechanics.
Everything else should stay in prompts, formulas, pack scripts, or `gc bd`
passthrough.

## Current conclusion

Gastown can now be represented primarily as packs, prompts, formulas, orders,
and bead operations. Remaining work is cleanup and a few possible convenience
surfaces, not a broad SDK buildout.

The current implementation already includes:

- Agent work loop: `gc hook`, `gc sling`, `gc prime`, `gc handoff`,
  `gc runtime drain-ack`, `gc runtime request-restart`.
- Session operations: `gc session list/attach/peek/kill/logs/nudge/reset`,
  plus `gc session submit` for semantic delivery.
- Coordination: `gc mail`, `gc convoy`, `gc workflow` alias, `gc order`,
  `gc events`, `gc event`, `gc formula`, `gc bd`.
- Rig and city lifecycle: `gc rig add/list/remove/restart/resume/status/suspend`,
  top-level `gc start/stop/restart/status/suspend/resume/reload`.
- Config infrastructure: prompt template rendering, prompt fragments,
  `pre_start`, `session_live`, `session_setup`, custom `session_template`,
  named sessions, imports, pack commands, doctor scripts, and runtime
  fingerprinting.
- Health/ops: `gc doctor`, `gc trace`, `gc dashboard`, `gc beads`, Dolt
  cleanup/config/state helpers, supervisor registration.

## Remaining SDK candidates

These are the only currently visible SDK candidates from the Gastown audit.

| Candidate | Why it might belong in Go | Current recommendation |
|-----------|---------------------------|------------------------|
| `gc context --usage` | Context-window utilization is provider-specific and belongs behind a session/provider boundary if exposed at all. | Keep as the only real feature candidate. First define the provider contract and confirm a current prompt needs it. |
| `gc mail hook <id>` | Could atomically convert a message bead into assigned work. | Do not implement yet. Current prompts use mail lifecycle plus bead assignment directly. |
| `gc mail send --human` | Convenience alias for a delivery channel. | Prefer `gc mail send human ...`. Add a flag only if backwards-compatible script sugar becomes useful. |
| Additional rig verbs: `start`, `stop`, `park`, `dock`, `unpark`, `undock`, `reboot` | Could make rig lifecycle vocabulary more expressive. | Do not add state-file-backed lifecycle. Current model is `suspend/resume/restart/status`; either keep prompts on those verbs or add aliases with the same semantics. |

## Cleanup roadmap

These are documentation/prompt tasks, not new primitives.

| Task | Reason |
|------|--------|
| Update `packs/gastown/agents/mayor/prompt.template.md` rig lifecycle wording. | It still talks about `stop/start` and `restart/reboot` even though the implemented rig verbs are `suspend/resume/restart/status`. |
| Keep `FUTURE.md` and this file in sync. | `FUTURE.md` is now the current gap inventory; this file should stay higher-level. |
| Remove old "pure gt parity" assumptions from future planning. | Role-specific commands are intentionally not SDK primitives. |

## Shipped since the old roadmap

The original roadmap listed these as future work. They are now implemented
and should not be re-designed from scratch.

### Core agent loop

- `gc hook [agent]` runs the configured `work_query`.
- `gc sling [target] <bead-or-formula-or-text>` routes work, can create a
  task from inline text or stdin, can instantiate formula wisps, supports
  `--on`, and can create workflow/convoy structure.
- `gc handoff` sends durable handoff mail and coordinates restart for
  controller-managed sessions; `gc handoff --auto` exists for provider
  compaction hooks.
- `gc runtime drain-ack` and `gc runtime request-restart` are the canonical
  agent-exit/context-refresh surfaces.

### Session inspection and nudge

- The old proposed `gc peek` is implemented as `gc session peek <target>
  --lines N`.
- The old proposed top-level nudge command is implemented as `gc session nudge
  <target> <message...>`.
- Delivery modes exist via `gc session nudge --delivery
  immediate|wait-idle|queue`.
- Deferred nudge inspection/delivery exists under `gc nudge status`, plus
  hidden runtime hook commands.

### Event watch and orders

- `gc events --watch` exists, with `--type`, `--timeout`, `--after`,
  `--after-cursor`, and payload filters.
- `gc events --follow` exists for continuous streaming.
- Orders are first-class `orders/*.toml` files, surfaced through
  `gc order list/show/run/check/history/sweep-tracking`.
- The controller evaluates orders and dispatches due work as wisps or exec
  scripts.

### Mail lifecycle

Mail is no longer "partially implemented." Current subcommands include:

- `send`, `inbox`, `read`, `peek`, `reply`, `thread`.
- `archive`, `delete`, `mark-read`, `mark-unread`.
- `check`, `count`.
- `send --notify` and `reply --notify`.

Remaining mail possibilities are only `mail hook` and `send --human`, both
convenience candidates rather than required SDK surfaces.

### Convoys and workflows

Convoys are implemented as graphs of related work beads. Current commands
include:

- `gc convoy create/list/status/target/add/close/check/stranded/land`.
- `gc convoy delete/delete-source/reopen-source`.
- `gc convoy control` and `gc convoy poke` for control-dispatcher plumbing.
- `gc workflow` as a deprecated alias for convoy/control operations.

This closes the old "where do convoys belong?" design question for the
current implementation.

### Config infrastructure

- Prompt templates ending in `.template.md` are rendered with Go
  `text/template`.
- Shared prompt fragments and pack-level `template-fragments/` are loaded.
- Prompt frontmatter metadata and rendered prompt hashes are tracked.
- `[[agent]].pre_start` exists, is template-expanded, is included in runtime
  fingerprints, and is executed by tmux before session creation.
- `[workspace].session_template` controls session names.
- Named sessions model always-on/on-demand role identities without
  hardcoding roles in Go.

### Health

- `gc doctor [-v|--verbose] [--fix]` exists and has grown into the general
  city/workspace health check.
- Crash/restart behavior is controller-owned and configured through daemon,
  lifecycle, session, and agent settings rather than role-specific commands.

## What should not become SDK code

These are resolved as raw bead operations, prompt/formula protocol, pack
scripts, or generic controller/session behavior.

| Old idea | Current route |
|----------|---------------|
| Role-specific commands such as `gc polecat`, `gc dog`, `gc boot`, `gc mayor`, `gc deacon` | Generic session, runtime, rig, mail, convoy, order, and bead operations. |
| `gc done` | Git push/metadata/status updates, optional refinery routing, then `gc runtime drain-ack`. |
| `gc escalate` | `gc mail send <target> -s "ESCALATION: ..."` and durable bead metadata when appropriate. |
| `gc mq ...` | Merge-request beads, refinery formulas/prompts, git workflow, and bead metadata. |
| `gc warrant file ...` | `gc bd create --type=task --label=warrant --metadata ...` routed to dog. |
| `gc compact` / `gc patrol digest` | `gc bd` queries plus maintenance orders and scripts. |
| `gc worktree ...` | `pre_start` worktree setup scripts and raw `git worktree` where needed. |
| `gc feed ...` | `gc events --since`, `gc events --watch`, or `gc events --follow`. |
| `gc costs` | Removed; provider-specific and not an SDK primitive. |
| Agent bead protocol commands | Session beads, metadata, labels, `gc session`, and `gc bd`. |
| Gates namespace in `gc` | `gc bd gate ...` passthrough. |
| Molecule namespace in `gc` | `gc bd mol ...` passthrough plus hidden `gc wisp autoclose` infrastructure. |

## Primitive test for future roadmap items

Before adding any new `gc` command for Gastown, check:

1. Does it cross the session/provider boundary?
2. Does it require controller-owned lifecycle or reconciliation?
3. Does it need atomic multi-bead/workflow mutation that prompts cannot make
   reliable?
4. Does it need typed HTTP/SSE/API behavior?
5. Is it a reusable config/rendering/materialization mechanism?

If the answer is no, keep it in pack configuration, prompt text, formulas,
scripts, or `gc bd`.

## Updated scope estimate

The old roadmap estimated about 1,200 lines of Go. That work has mostly
landed. The remaining possible SDK scope is small:

| Item | Rough scope | Priority |
|------|-------------|----------|
| Provider-backed `gc context --usage`, if retained | 100-200 lines plus provider contracts | Low until a prompt needs it |
| Optional `gc mail hook` | 20-60 lines | Low |
| Optional `gc mail send --human` alias | 10-30 lines | Low |
| Optional rig verb aliases | 50-150 lines, depending on semantics | Low/UX-driven |

The bigger remaining work is documentation hygiene, not SDK implementation.
