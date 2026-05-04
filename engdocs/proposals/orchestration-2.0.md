# Orchestration 2.0

## Summary

Gas City is an orchestration-builder SDK — a Go toolkit for composing multi-agent coding workflows. The current orchestration model (formulas v1) treats a formula as a recipe that instantiates a tree of beads (a molecule) per single bead of work. Convoys exist as a runtime side-effect for batch tracking. Scatter/gather is emergent from metadata-driven control beads with a hardcoded any-fail→fail policy. There is no first-class human-in-the-loop. Sessions, molecules, convoys, wisps, and orders are five separate concepts with five separate viewers and five separate APIs.

Real users describe their work in *artifacts* (PRs, issues, plans, markdown) and *phases* (implement → review → test → ship), but the formula model forces them to pre-shred work into beads and run one formula per bead. The pseudo-convoy hack — stuffing everything into a single bead with a link to a doc — recovers some of the missing structure but throws away parent/child/dep relationships. Multi-reviewer review patterns recur across packs but have to be copy-pasted because there is no real composition mechanism.

Orchestration 2.0 closes the gap. The primitive is a **Run** — a first-class, addressable, observable execution of a formula over a convoy. Sub-formulas compose via inline expansion. Human-in-the-loop is a runtime disposition class with its own pause / notify / authorization machinery. Sessions are Runs of a one-step formula, so observability is unified across structured workflows and ad-hoc agent activity. The five separate concepts of v1 collapse into one.

---

## Goals

### Goal — Close the gap between user mental model and execution model

Real users think in *artifacts* and *phases*. They have a markdown plan, a GitHub issue, or a PR description; they want it implemented across multiple parallel work units; they want the output reviewed as a whole; they want to gate irreversible actions on human approval; and when an agent gets stuck, they want it to ask a human for help. The current model fights all of those — work has to be pre-shredded into beads, reviews run per-bead instead of per-output, HITL is bolted on, escalation is ad-hoc.

O2 makes these patterns first-class. Formulas operate over convoys. Shredding is a composable sub-formula authors drop in. Multi-reviewer scatter/gather is first-class with author-controlled gather policies. Human-in-the-loop is a runtime disposition the orchestrator understands, available both as a planned step at design time and as a reactive escalation any agent can trigger at runtime.

### Architectural payoff — Unify execution primitives under Runs

Five separate concepts in v1 (sessions, molecules, convoys, wisps, orders) collapse into one in O2 — the Run. A Run is a first-class persistent entity addressable by ID, observable from one viewer, queryable through one API, and recoverable from beads on controller restart. Sub-formula composition expands inline into the parent Run, so nested workflows are just expanded step graphs with provenance metadata, not separate sub-executions.

This is the architectural elegance that makes the user-facing improvements tractable. One execution model means one observability surface, one mutation API, one crash-recovery path, one debugging tool. Without this unification, every user-facing feature would have to be implemented four times across four siloed concepts.

---

## User Scenarios

### S1. Formulas operate over a convoy as their input.

A formula is invoked with a convoy of related beads as its input — the whole convoy, not one bead. The formula author chooses, per step, whether to treat the convoy as an indivisible unit (one prompt to one agent that sees all the work at once) or to break it up for parallelism via a drain loop that spins up polecats to handle ready beads, drains them, and repeats until the convoy is fully processed. Bare-bead invocations are normalized by the runtime into a 1-element convoy, so the existing per-bead workflow becomes the degenerate case rather than a separate code path.

*Forces:* convoy-as-input as the formula's input contract; bare-bead invocations auto-normalized into a 1-element convoy at Run start; drain-loop semantics as an author-declared choice per step; explicit "as a whole" vs "decompose" intent at the step level.

### S2. Easy shredding of a planning artifact into a convoy of beads.

A user starts a workflow with a single bead that links to or references a planning artifact — a markdown plan, a GitHub issue, a PR description. The formula author chooses what happens: pass the bead straight through to an agent that handles the artifact as-is, or transform it into a real convoy of beads with parent/child/dep structure for subsequent steps to operate on. Authors who want to shred get a built-in capability they can drop in; authors with different needs roll their own.

*Forces:* ship an OOB shredding sub-formula in the stdlib covering common artifact types (markdown, GitHub issues, PR descriptions); sub-formulas must be importable and invocable from formula TOML; inline steps must have an idiom for producing structured convoys as output; "step produces convoy" → "next step consumes convoy" is a typed handoff.

### S3. Multi-reviewer scatter/gather across a convoy.

A formula phase fans out the same unit of work — typically the convoy produced by an implementation phase — to multiple specialized reviewer agents in parallel: lint, security, design, performance, integration tests. Each reviewer produces a verdict; the gather phase combines the verdicts into a single phase outcome under an author-declared policy (e.g., pass when 4+ of 5 pass, degraded when 2-3 pass, hard-fail when fewer). Authors compose specialized reviewers without writing coordination glue, and the gather policy is theirs to choose, not the runtime's.

*Forces:* gather policy is author-declared (declaratively in TOML for thresholds/weights, or as an agent step inside the sub-formula for judgment-based policies); today's hardcoded any-fail→fail in `internal/dispatch/fanout.go` becomes a policy default, not a runtime constant; ship an OOB scatter/gather sub-formula in the stdlib; the existing `soft_fail` disposition (narrow to retry exhaustion in `internal/dispatch/retry.go`) is renamed `degraded` and generalized to be a valid outcome of gather phases.

### S4. Planned human-in-the-loop checkpoint.

A formula reaches a designated checkpoint — typically before an irreversible action like deploying to production, opening a public PR, or merging to main — and pauses for an authorized human to approve, reject, or comment. The Run resumes on approval, terminates with a clean failure on rejection, and surfaces the pending decision so the human knows it's their turn.

*Forces:* HITL is a runtime disposition class — a new step state alongside transient-fail and hard-fail, with its own scheduling, notification, and authorization policy applied by the orchestrator (not a sub-formula); formula authors declare HITL steps in TOML to invoke the primitive at compile time (S5 is the runtime entry point — same primitive, two entry points); a notification mechanism surfaces pending HITL steps to the assigned human; an authorization model exists for who is allowed to approve; rejection terminates the Run via the hard-fail path, which downstream steps can react to.

### S5. Reactive escalation for human help mid-Run.

An agent mid-execution hits something it can't resolve on its own — a rate limit, an ambiguous spec, a missing credential, a contested decision — and signals that the current step needs human input by transitioning to the HITL state. The Run pauses on this step (other ready work continues if independent); the orchestrator surfaces the pending human action; once the human responds, the step resumes with the new context. The agent doesn't restructure the graph or spawn anything — HITL is a step disposition the orchestrator already understands.

*Forces:* HITL is a new step disposition between transient-fail and hard-fail (the runtime policy distinguishes "retry," "give up," and "wait for human"); agents can transition any in-flight step to HITL state at runtime by reporting that disposition; the orchestrator routes HITL-state steps through the same notification and authorization machinery as the planned HITL of S4 — same primitive, two entry points (declared at compile time vs reported at runtime); on human response, the step resumes in the same Run with the new context available to the agent.

### S6. Runs as a first-class concept, visualized in the dashboard.

A user opens the dashboard and sees Runs as the central object — every formula execution is a Run with a current state, steps, and bead activity they can watch and drill into. Sessions are Runs of a single-step "execute" formula, so ad-hoc agent activity shows up alongside structured workflow Runs without a separate session viewer. When a Run uses sub-formulas, the viewer surfaces the logical grouping of steps by which sub-formula invocation produced them, so users can trace where each step came from inside a single Run.

*Forces:* Run is a first-class persistent entity (addressable, queryable, observable); a Run visualization exists in the dashboard with current state, steps, and bead activity; sessions are modeled as Runs of a built-in single-step "execute" formula (one mechanism, not two); steps carry sub-formula provenance metadata so the viewer can render logical grouping within a single Run.

### S7. Long-running Run with HITL pause across controller restarts.

A Run pauses at a HITL checkpoint for three days. The controller restarts twice during that time. The human eventually approves; the Run resumes cleanly with no lost state, no duplicate work, and no orphaned polecats.

*Forces:* all Run state lives in beads (no status files, no in-memory-only state); controller crash-recovery handles paused Runs identically to live Runs (no special path); pending-checkpoint notifications survive controller restart (notification source is durable state); polecats don't leak across pauses (clean shutdown when work isn't ready).

### S8. Sub-formulas as a reusable library.

A formula author imports a sub-formula by name and composes it into their workflow. O2 ships a stdlib library covering the sub-formulas the other scenarios commit to: shred-plan (S2) and scatter-gather (S3). Authors build their own sub-formulas — for organization-specific patterns or reusable fragments — and share them via packs; version pinning keeps consumers from breaking on upstream changes.

*Forces:* sub-formulas are addressable, namespaced, standalone units composed into parent formulas via import; each invocation expands inline into the parent Run's step graph (generalizing today's Expand/Map mechanism in `internal/formula/expand.go`); a stdlib library ships with O2, initial contents are shred-plan + scatter-gather; pack distribution mechanism extends to author-supplied sub-formulas; sub-formula version pinning prevents breaking on upstream changes; ergonomic import syntax in formula TOML.

### S9. Mid-Run operator inspection and intervention.

A Run is in flight. An operator opens the dashboard, drills into a specific bead, sees what the agent is doing right now, and intervenes — re-prompts an agent, marks a bead skipped, kills a stuck polecat, adjusts a label. The Run continues from the new state without manual stitching.

*Forces:* the unified viewer is read+write, not display-only; mutation operations (skip / abort / re-prompt / relabel / kill-polecat) are first-class with defined downstream semantics; mutations emit events (auditable, who-did-what surfaces in the viewer); "query live state" reflects operator mutations immediately.

---

## Requirements

### Run lifecycle and state model

**R1. Runs are first-class, persistent, addressable entities.** Every formula execution — whether triggered by a user, an order, or another agent — produces a Run with a stable ID, current state, and full lineage of steps and beads. Runs are queryable individually and listable in aggregate. *(S6, S7, S9)*

**R2. All Run state is durable in beads.** No status files, no in-memory-only state. A controller restart at any point during a Run's lifetime must result in the Run resuming from the same state it was in before the restart, with no lost work and no duplicated work. *(S7)*

**R3. Sessions are Runs.** Ad-hoc agent activity is modeled as a Run of a built-in single-step "execute" formula. There is one execution model in the data layer; "session" is a UI affordance, not a separate type. *(S6)*

**R4. The Run state machine includes a paused-awaiting-input state.** The orchestrator distinguishes "running," "paused awaiting human input," and terminal states. A paused Run is not stuck-looking and is not a candidate for stale-Run garbage collection. *(S4, S5, S7)*

**R5. Operator mutations on a Run preserve integrity.** Skip, abort, re-prompt, relabel, and kill-polecat are first-class operations with defined downstream semantics. Each mutation emits an event recording who performed it and what changed. The semantics for each operation under composition (skip-of-scattered-step, abort-of-parent-with-active-HITL, etc.) are specified in TC8. *(S9)*

### Convoy and step input semantics

**R6. Formulas operate over a convoy as their input contract.** Every formula invocation receives a convoy. Bare-bead invocations are normalized by the runtime into a 1-element convoy before the first step sees it. *(S1)*

**R7. Step input mode is author-declared per step.** A step declares whether it processes its input convoy as an indivisible unit (one prompt, agent sees all) or via a drain loop (parallel polecats per ready bead, drain, repeat). The default applies when nothing is declared. *(S1)*

**R8. Convoys produced by steps carry parent/child/dep structure.** When a step's output is a convoy (e.g., a shred step), the convoy is structured (real parent/child/dep edges in the bead graph) and consumed downstream as a typed handoff, not as a side-effect. *(S2)*

### Sub-formula composition

**R9. Sub-formulas are addressable, namespaced, standalone units.** Sub-formulas have names, versions, and pack identity; they are imported by name in formula TOML and not embedded inline as code. *(S8)*

**R10. Sub-formula invocation expands inline into the parent Run.** Each invocation contributes steps to the parent Run's step graph, generalizing today's Expand/Map mechanism in `internal/formula/expand.go`. No separate Run is created. *(S8)*

**R11. Steps record sub-formula provenance.** Steps produced by a sub-formula invocation carry metadata identifying which sub-formula and which invocation produced them. This metadata is what the viewer uses to render logical grouping within a Run. *(S6, S8)*

**R12. Agents can trigger sub-formula expansion at runtime.** The same import mechanism formula authors use is available to agents at runtime, so reactive escalations and dynamic graph extension don't require a parallel API. *(S5)*

### Step types and dispositions

**R13. The orchestrator recognizes a HITL disposition class.** HITL is a runtime disposition alongside transient-fail and hard-fail, with its own scheduling, notification, and authorization policy. Steps can enter HITL state by author declaration (compile time) or by agent transition (runtime). *(S4, S5)*

**R14. Pending HITL steps are surfaced to the assigned human.** The orchestrator notifies the assigned human via a defined notification mechanism when a step enters HITL state. The notification source is durable (survives controller restart). *(S4, S7)*

**R15. HITL approvers are subject to an authorization model.** "Who is allowed to approve" is verifiable at approval time. Rejection terminates the Run via the hard-fail path; downstream steps can react to it. *(S4)*

**R16. Gather phase outcomes follow author-declared policy.** A gather phase combines its children's outcomes via a policy declared by the author — declaratively in TOML for thresholds and weights, or as an agent step inside the sub-formula for judgment-based policies. The runtime applies the policy; the runtime does not decide it. *(S3)*

**R17. The third disposition is named `degraded` and is available beyond retry exhaustion.** The disposition currently called `soft_fail` (narrow to retry exhaustion in `internal/dispatch/retry.go`) is renamed `degraded` and generalized to be a valid outcome of gather phases and any other step type that needs to express "succeeded with reduced coverage." *(S3)*

### Polecat drain and concurrency

**P1. Per-Run drain coexists with per-agent pool caps.** A Run's drain loop acquires polecat capacity from the existing agent pools (`MaxActiveSessions` / `MinActiveSessions` at agent / rig / workspace levels in `internal/config/config.go:1639-1655`). The drain loop does not exceed those caps; it requests slots and waits when capacity is unavailable. `ScaleCheck` continues to drive dynamic scaling within the caps. *(S1)*

**P2. Drain completion is defined as quiescence over the convoy's ready set.** A drain is complete when the convoy has no remaining ready beads and no in-flight polecats are processing beads from this convoy. Beads created after drain completion belong to subsequent steps, not this drain. *(S1)*

**P3. Recursively-discovered work within an active drain joins the same drain.** When a polecat creates new beads during a drain (transitively discovered work that belongs to the same convoy graph), the drain picks them up in subsequent waves until quiescence. This is what makes the drain a loop, not a single fan-out. *(S1)*

**P4. Polecat lifetime is per-bead, not per-drain.** A polecat acquires the next ready bead from its drain queue, processes it, returns the slot to the pool, and either picks up the next bead or is recycled per the agent pool's normal lifecycle. There is no drain-scoped polecat that lives for the entire drain. *(S1)*

### Stdlib library

**R18. O2 ships a stdlib sub-formula library.** Initial contents: a shred-plan sub-formula covering common artifact types (markdown plans, GitHub issues, PR descriptions); a scatter/gather sub-formula supporting both declarative and agent-driven gather policies. *(S2, S3, S8)*

**R19. Pack distribution extends to author-supplied sub-formulas.** The mechanism that distributes formulas in packs also distributes sub-formulas. Authors share sub-formulas through the same channels they share formulas. *(S8)*

**R20. Sub-formula version pinning is supported.** Consumers pin to a specific version of a sub-formula in their import; upstream changes don't break consumers until they explicitly bump the pin. *(S8)*

### Observability

**R21. The dashboard renders Runs as the central object.** Active and historical Runs are listable, queryable, and drillable. Run detail shows current state, steps, bead activity, and (when sub-formulas are used) logical grouping of steps by their sub-formula provenance. *(S6)*

**R22. Operator mutations reflect immediately in live state.** A mutation through the dashboard is queryable through the same API immediately afterward; there is no read-after-write inconsistency window for operator-initiated state changes. *(S9)*

---

## Technical Considerations

### TC1. Typed dispositions (foundation)

Today, every state machine in dispatch is built on stringly-typed metadata: `gc.outcome=pass|fail`, `gc.failure_class=transient|hard`, `gc.fanout_state=spawning|spawned`, `gc.retry_state=spawning|spawned`. Producers and consumers don't share a Go type. Invalid combinations are theoretically possible; the compiler catches none of them. Real evidence: `internal/api/handler_convoy_dispatch.go:487` reads `bead.Metadata["gc.outcome"]` as a raw string; `internal/api/huma_handlers_convoys.go:535` writes `map[string]string{"gc.outcome": "skipped"}`. Grep is the only enforcement.

**Direction.** Introduce a typed Disposition ADT (sum type) in Go. Each variant carries its own typed payload:

- `Pass{result}`
- `HardFail{reason}`
- `Transient{retries_remaining, last_error}`
- `Degraded{coverage_explanation, partial_results}`
- `HITL{assigned_human, request, auth_policy, deadline}`

The runtime's policy table dispatches on the variant. Producers and consumers share a single Go type. Huma OpenAPI generation makes the wire format honest; the dashboard's TypeScript types are generated from the same source. Bead metadata becomes the *projection* of the typed disposition, not the source of truth.

This is foundational. It unblocks: gather policy (configurable based on typed children's dispositions in TC6), HITL primitive (adds a variant), `degraded` as a first-class outcome (existing variant generalized beyond retry exhaustion), Agent ABI return shape (TC3).

### TC2. Run as a bead

A Run should be a typed bead (`Type="run"`), not a projection over existing beads and not a parallel persistence model. This honors the "beads is the universal persistence substrate" invariant. Crash recovery falls out for free — the controller already knows how to rebuild reality from beads. The API surface is `GET /beads?type=run` and `GET /beads/{id}` with no new persistence APIs. Sub-formula expansion remains "child beads with the parent Run's `gc.root_bead_id`."

The one open question is structured payload. Bead metadata is currently flat string key/value; a Run carries structured data (vars resolved at instantiation, formula version, current step, state machine position, the input convoy reference). Two viable designs:

- **(a) Flat string conventions with a typed schema.** Producers and consumers share a Go type; serialization to bead metadata happens at the boundary. Reuses existing bead infrastructure as-is.
- **(b) "Vars bead" linked to the Run.** Carries the structured payload as a typed JSON blob; the Run bead points to it.

Recommend (a) initially; the typed Disposition system from TC1 provides the same boundary-typed-internally pattern. Revisit if metadata growth becomes painful.

### TC3. Agent ABI

The most important interface in the system, currently the most under-specified.

**Today** the agent contract is implicit and scattered. The agent's input is the union of (the bead it claims via `WorkQuery`) + (the prompt template's content) + (its working directory and environment). The agent's output is the union of (metadata it sets on closed beads — `gc.outcome` and friends) + (any new beads it creates) + (any session-level interactions raised via `PendingInteraction` in `internal/runtime/runtime.go:199`). There is no single document or type that says "this is the contract."

**Direction.** A typed, versioned Agent ABI built around the metaphor of a *capability-based context*. When the runtime hands work to an agent, the handoff carries:

- **Run context:** the Run ID, formula identity, formula version, and the resolved variable scope (per TC4).
- **Work scope:** the work bead (or convoy if the step is convoy-aware), parent step's output if this is a downstream step, and step-specific metadata.
- **Available primitives:** typed primitive functions the agent can invoke during its work — `report_disposition(disposition)`, `expand_sub_formula(name, vars)`, `request_hitl(request)`, `produce_convoy(beads)`, `create_child_bead(spec)`. Each primitive has a typed signature.

When the agent finishes, it returns a typed `Disposition` (per TC1) plus any `produced_artifacts` (new beads, convoys, structured outputs). The wire shape is one typed envelope, not the union of "set this metadata, also create those beads, also raise this interaction."

**Versioning.** The Agent ABI carries a major.minor version. Adding a new disposition variant or a new primitive is a minor bump; agents built against an older minor version still work because they ignore unknown primitives and never produce unknown dispositions. Removing a primitive or a disposition is a major bump and requires explicit migration.

**Inspirations.** LSP's typed RPC with versioned capabilities is the right shape but the wrong runtime model (agents aren't long-lived servers). Claude Code's model→tool ABI is closer — the agent receives a typed context with available tools, decides what to do, and returns structured results. Worth borrowing the capability-discovery pattern and the "you can only invoke what's been advertised" discipline.

**Why this matters.** Get the ABI right and every downstream feature gets easier — gather policies become "agents that consume children's typed dispositions," HITL becomes "agents that return the HITL variant," sub-formula expansion becomes "agents that invoke `expand_sub_formula`." Get it wrong and every feature has to be implemented twice (once for the runtime, once for the agent's view of it).

### TC4. Data flow and variable scoping

When a sub-formula expands inline into a parent Run, what variables does it inherit from the parent? What can it shadow? When two sub-formula invocations in the same Run produce variables with the same name, what wins? When a step's output (e.g., a convoy from shred-plan) is consumed by the next step, what's the named handoff?

The proposal commits to typed handoffs (R8) and ergonomic sub-formula imports (R9, R20) but does not define the data-flow model. This is pervasive and will determine ergonomics across the entire authoring experience.

**Spectrum of options:**
- **Implicit ambient (env-var style).** All vars in scope are visible everywhere. Easy for authors; collisions are catastrophic.
- **Explicit per-invocation (React props / function args).** Sub-formulas receive only the vars passed to them; outputs flow back through declared returns. Verbose but unambiguous.
- **Dependency injection (declared, runtime-resolved).** Sub-formulas declare what they need; the runtime resolves from scope. Middle ground.
- **Dataflow language (explicit edges).** Author wires step outputs to step inputs explicitly. Most powerful, steepest learning curve.

**Direction.** Explicit per-invocation as the default, with a small sugar for "pass through everything" for trivial cases. Sub-formulas declare their input variable schema; consumers pass values explicitly. Step output is named (`outputs.convoy`, `outputs.disposition`) and accessed by name in downstream steps. Variable namespacing per sub-formula invocation prevents collisions across siblings.

**ZFC-aligned move:** the runtime routes typed values, never makes data-flow judgment calls. Authors declare; runtime carries.

**Open question worth addressing in design phase:** the "drain loop" has a different data-flow shape than serial steps — each polecat sees one bead from the convoy, not the whole convoy. The data-flow model needs to express both single-bead and convoy-as-whole step inputs uniformly.

### TC5. Check and retry unification

Today, `[steps.check]` (validation-driven retry, the existing pattern) and `[steps.retry]` (failure-driven retry, the v2 transient-retries spec) are mutually exclusive at the step level, enforced at compile time. A step can have validation-driven retries OR failure-driven retries, not both. There is no architectural reason for this — it is a historical artifact from two designs landing at different times. Real-world steps want both ("retry on rate-limit AND validate the result on success and retry on validation failure").

**Direction.** Unify under a single resilience model with composable predicates. A step has:

- **Continue conditions** — predicates that all must hold for the step to be considered done (work succeeded, validation passed, no transient failure detected).
- **Retry policies** — max attempts, backoff, on-exhausted disposition.

Either dimension can be empty; both can be present; combinations are well-defined. Existing v1 formulas continue to work via the formula version gate; new formulas opt into the unified model.

### TC6. Gather policy expression

R16 commits to author-declared gather policies "in TOML for thresholds/weights, or as an agent step for judgment-based policies." The TOML expression shape is undesigned. If the schema is too rigid, authors flee to "agent step" mode for everything and the declarative path is dead. If too flexible, you've reinvented Rego or CEL with worse ergonomics.

**Direction.** A small, sharp policy language built on the typed Disposition system from TC1. Predicates over typed children's outcomes:

```toml
[gather.policy]
pass_when = "count(d.kind == 'pass') >= 4"
degraded_when = "count(d.kind == 'pass') >= 2"
hard_fail_when = "default"
```

For weighted policies:

```toml
[gather.policy]
weights = { security = 2.0, integration = 2.0, lint = 0.5 }
pass_when = "weighted_sum(d, weights) >= 3.0"
```

Expressive enough for real cases, narrow enough to avoid Turing-completeness. Could borrow from the existing `condition = "{{var}} == value"` step condition syntax in `internal/formula/condition.go` and extend it.

**Escape hatch:** when the declarative language doesn't suffice, the gather policy is an agent step inside the scatter/gather sub-formula. The agent receives the typed children's dispositions and returns a single phase disposition. Same Disposition ADT (TC1) end-to-end.

### TC7. HITL as a family of Human Tasks

The proposal frames HITL as approve / reject / comment (S4, S5). Real human-in-the-loop workflows include richer interactions:

- **Approve.** Binary or multi-state approval (yes / no / yes-with-conditions).
- **Choose.** Pick one of N options ("Here are 3 candidate fix approaches; pick one").
- **Provide.** Supply data the agent doesn't have (a credential, an environment-specific value).
- **Author.** Write content the agent will execute against (a migration plan, a copy edit).
- **Annotate.** Review an artifact and mark concerns inline.
- **Confirm.** Acknowledge intent before an irreversible action.

**Direction.** The HITL disposition variant from TC1 is a *discriminated union of Human Task types*, not a single Approval Request:

```
HITL{
  Approve{question, options, context}
  | Choose{question, options[], context}
  | Provide{prompt, expected_schema, context}
  | Author{prompt, template, context}
  | Annotate{artifact, instructions, context}
  | Confirm{action_description, context}
}
```

Same pause / notify / authorization / durability machinery for all of them; different interaction shapes in the dashboard and different return-value shapes in the agent's resumed context. Authors declare which Human Task at the step (S4) or the agent reports the variant at runtime (S5).

**Scope discipline.** For O2 v1, commit to `Approve` and `Choose` as the initial Human Task variants. `Provide`, `Author`, `Annotate`, `Confirm` land in follow-on iterations. The discriminated-union shape is set up from the start so adding variants is additive, not a rewrite.

**Authorization & notification.** Single notification channel (dashboard) for v1. Basic auth model: approver is named in the formula, or in a configured approver list. Multi-channel notification (email, Slack), presence detection, and escalation chains are explicitly deferred.

### TC8. Operator intervention semantics

R5 commits to skip / abort / re-prompt / relabel / kill-polecat as first-class operations. Each has subtle semantics under composition that need specification.

**Skip.** Marks a step as terminally skipped. Downstream steps that `needs` the skipped step see disposition `Skipped` (a new disposition variant; possibly a sub-variant of `Degraded`). Steps that condition on the skipped step's success treat skipped as fail.

**Abort.** Terminates a Run. All sub-formula expansions in the Run are also aborted (their sub-trees inherit the parent's terminal state). In-flight polecats receive a graceful drain signal (`DrainTimeout`); already-completed work stays completed.

**Re-prompt.** Sends a new prompt to an agent currently working on a step. For HITL steps, re-prompt means "change the question" — the existing pending Human Task is canceled and a new one is created with the new prompt. For working agents, re-prompt is a course-correction.

**Relabel.** Adjusts labels on a bead. If the relabeled label affects routing (`pool:reviewers` → `pool:senior-reviewers`), in-flight work continues with its current pool; new work picks up the new pool. Re-routing in-flight work requires explicit cancel + re-dispatch.

**Kill-polecat.** Kills a specific polecat process. The work the polecat was doing is marked transient-fail (per the existing transient retry machinery) and either retried (if the step has a retry policy) or marked degraded (if not).

**Implementation pattern.** Each intervention is a typed event in the bead store; the runtime reduces over it the same way it reduces over agent-produced state changes. This keeps the "no status files; query live state" invariant intact while making interventions auditable, reversible, and composable.

### TC9. Visualization for deep, long-running Runs

R21 commits to the dashboard rendering Runs as the central object. The proposal does not grapple with the visualization problem at scale — Runs with hundreds of beads from sub-formula expansion + scatter children + retries, spanning hours or days with HITL pauses.

**Existing concurrency baseline.** Three-tier session caps, dynamic `ScaleCheck` per agent, `MaxWakesPerTick` reconciler limit, `DrainTimeout` for graceful scale-down, `MaxRestarts` budget. These continue to apply. O2's drain loop composes onto them via P1.

**Unaddressed at scale:**
- Viewer rendering for deep Runs.
- Hot bead store keys (Run root, parent of a deep tree get queried frequently).
- GC of completed Runs (when does a closed Run get archived; what's the retention policy).
- Event firehose (a long-running Run with HITL pauses can produce hundreds of state-change events; do all of them get retained).

**Visualization direction.** A Run's UI is not one view but several projections, each showing a different facet:

- **"What's happening now"** — the active steps, their assignees, what each polecat is doing right now.
- **Timeline** — duration-aware view of when each step started, paused, resumed, completed.
- **Logical hierarchy** — collapse-by-sub-formula tree showing nested invocations.
- **Dependency graph** — typed edges between beads / steps for debugging dependency issues.
- **Time-spent analysis** — a flame-graph-style view showing where time was spent (work vs HITL pauses vs queue waits).

**Inspirations.** Honeycomb's BubbleUp, Jaeger's trace viewer, GitHub Actions' nested-job view, Sentry's issue grouping. Each solves a piece of this. Workflow visualization at this scale doesn't have an off-the-shelf answer — genuinely fertile design ground.

**Storage direction.** Tier the bead store (hot for active Runs, warm for recently completed, cold for archived). Compute viewer state on read with caching, not on write. Adopt event-sourcing patterns for Run state where the bead store is the log and viewers are projections. Adaptive event filtering for very deep Runs.

This section is intentionally a sketch. Detailed design is deferred to a follow-on; the proposal commits to addressing it before O2 ships at production scale.

### TC10. Sub-formula ecosystem hygiene

R19 and R20 commit to pack distribution and version pinning. The ecosystem questions go further:

- **Discovery.** How does a formula author know what stdlib sub-formulas exist? What contracts they have? `gc formula list-stdlib`? Schema introspection? Generated docs?
- **Compatibility ranges.** Pinning to one version means consumers don't get fixes. Semver ranges (`^1.2`)? Compatibility tags?
- **Deprecation.** When a sub-formula author deprecates v1 in favor of v2, how is that communicated to consumers?
- **Testing against versions.** Pack authors who depend on stdlib sub-formulas — how do they test against the latest?

**Direction.** Treat sub-formulas as ecosystem citizens, not internal implementation details. Borrow from package managers that have solved this: Cargo / npm / Go modules for versioning patterns; Helm charts and Terraform modules for the closer "it's not code, it's behavior" shape. Specific commitments:

- Every sub-formula declares a manifest with name, version, input variable schema, output schema, dependencies on other sub-formulas.
- `gc formula stdlib` lists available stdlib sub-formulas with their manifests.
- Imports support semver ranges; `gc formula update` opts into compatible upgrades.
- Deprecated sub-formulas emit warnings on import; the manifest can declare a successor sub-formula for migration.

**Humans aren't agents (companion design problem).** Today there is no `human` type in the codebase. The "Message represents a mail message between agents or humans" comment in `internal/mail/mail.go:34` is aspirational; no code distinguishes them. The HITL primitive (TC7) requires a human collaboration subsystem with its own primitives — Approver Pool, Notification Channel, Authorization. Don't reinvent IAM; leverage agent identity for system actors, add human-specific primitives that agent identity doesn't carry. Existing `PendingInteraction` / `InteractionResponse` types in `internal/runtime/runtime.go:199` are inspiration for the wire shape, not the actual mechanism — they live at the session/provider layer, which is a different layer than the orchestrator / Run-level HITL O2 needs.

### TC11. Migration and backwards compatibility

Formula version field gates v1 vs v2 behavior at the formula level. Existing v1 formulas execute on v1 semantics; new formulas opt into v2. No data migration of existing molecules / convoys / sessions / wisps required for the upgrade.

The `slingDefaultFormula` path in `internal/sling/sling_core.go:259` continues to work — it instantiates a v1 wisp around a single bead the way it does today. v2 formulas that take a single bead receive it as a 1-element convoy via runtime auto-normalization (R6).

One inline-author tool needs to land for S2 to work without forcing the OOB shred sub-formula: an idiom for declaring "this step produces a convoy bead with parent/child/dep structure." Today no formula-author-facing primitive expresses this. Proposed shape: a step output kind that the runtime recognizes as "this bead is a convoy root; its children are the structured work." This is part of the typed handoff in R8 and the data-flow design in TC4.

---

## Open Questions

1. **Default step input mode (R7):** should the default for an undeclared step be "as a whole" or "decompose"? Proposed default: "as a whole," because it preserves today's per-step semantics. Worth validating against early authoring experience.
2. **`degraded` rename rollout:** soft-fail is in production fixtures (`internal/testfixtures/reviewworkflows/fixtures.go`). Rename in v2 only; v1 keeps `soft_fail`.
3. **Scope of stdlib library:** ship v1 with shred-plan + scatter-gather (R18). Should `review-and-ship`, `deploy-with-canary`, or other common patterns be in v1 stdlib too?
4. **Formula schema location:** sub-formula version pinning syntax in TOML — where does the version field live? Per-import? Per-pack manifest?
5. **Notification channel for HITL v1:** dashboard-only for v1 per TC7. Confirm acceptable scope.

---

## Out of Scope

- **Multi-channel HITL notification** (email, Slack, etc.) — deferred to follow-on.
- **HITL presence detection** (is the assigned human available?) — deferred.
- **HITL escalation chains** (auto-route to fallback approver on timeout) — deferred.
- **Cross-Run coordination primitives** — Runs communicate via beads, never directly. If two Runs need to coordinate, that's two Runs touching the same beads.
- **Rewrite of the existing concurrency model** — O2's drain loop composes onto the existing `MaxActiveSessions` / `ScaleCheck` infrastructure (P1), not a replacement for it.
- **Detailed scale design** — TC9 is intentionally a sketch; detailed design lands in a follow-on before production scale.
