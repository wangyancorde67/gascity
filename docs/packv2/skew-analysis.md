# Spec vs. Implementation Skew Analysis

> Generated 2026-04-12 by comparing `docs/reference/config.md` (as-built
> from the release branch Go structs) against the reconciled pack v2 specs
> in `docs/packv2/`.

## How to read this

Each row is a field or concept where the as-built code diverges from the
reconciled spec. **Category** indicates the nature of the skew:

- **V1 remnant** — field exists in code but is removed or replaced in v2 spec
- **Not yet implemented** — spec describes something the code doesn't have yet
- **Placement mismatch** — field exists but in the wrong file per the spec
- **Naming mismatch** — field exists under a different name than spec prescribes
- **Convention gap** — code uses TOML declaration where spec says convention

---

## City (top-level struct)

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `include` | Present, []string | **Remove.** Replaced by `[imports]` | V1 remnant | |
| `workspace` | Required | **Remove from city.toml.** Identity → `.gc/`, composition → `pack.toml` | V1 remnant / placement | #600 (post-0.13.6) |
| `packs` | Present, map[string]PackSource | **Remove.** V1 mechanism, replaced by `[imports]` | V1 remnant | |
| `agent` | Required, []Agent | **Remove.** Agents discovered from `agents/<name>/` dirs | V1 remnant / convention gap | #608 |
| `formulas` | Present, FormulasConfig | **Remove.** `formulas/` is a fixed convention, `[formulas].dir` gone | V1 remnant | |
| `agent_defaults` | City-level only | Spec: **both** pack.toml and city.toml | Placement mismatch | |
| `imports` | Present ✓ | Present ✓ | OK | |
| `named_session` | Present in city struct | Spec: **pack.toml** | Placement mismatch | |
| `providers` | Present in city struct | Spec: **pack.toml** (usually) | Placement mismatch | |
| `patches` | Present ✓ | Present ✓ (both pack.toml and city.toml) | OK | |
| `rigs` | Present in city.toml ✓ | Present in city.toml ✓ | OK | |
| `beads`, `session`, `mail`, `events`, `dolt` | city.toml ✓ | city.toml ✓ | OK | |
| `daemon`, `orders`, `api` | city.toml ✓ | city.toml ✓ | OK | |
| `chat_sessions`, `session_sleep`, `convergence` | city.toml ✓ | city.toml ✓ | OK | |
| `service` | city.toml ✓ | city.toml ✓ | OK | |

## Workspace

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `name` | Required in city.toml | **Remove.** Derived from `pack.name` at registration, stored in `.gc/` | V1 remnant / placement | #600 |
| `prefix` | In workspace block | **Move to `.gc/`** — site binding | Placement mismatch | |
| `provider` | In workspace block | **Move to `[providers]` or `[agent_defaults]`** in pack.toml | Placement mismatch | |
| `start_command` | In workspace block | Per-provider or per-agent | Placement mismatch | |
| `suspended` | In workspace block | **Move to `.gc/`** — operational toggle | Placement mismatch | |
| `max_active_sessions` | In workspace block | city.toml deployment | OK (stays in city.toml) | |
| `session_template` | In workspace block | city.toml deployment | OK | |
| `install_agent_hooks` | In workspace block | Probably pack.toml | Placement mismatch | |
| `global_fragments` | Present | **Remove.** Replaced by `template-fragments/` + explicit inclusion | V1 remnant | |
| `includes` | Present | **Remove.** Replaced by `[imports]` in pack.toml | V1 remnant | |
| `default_rig_includes` | Present | **Replace with `[defaults.rig.imports]`** in pack.toml | V1 remnant / naming | |

## Agent

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| (entire `[[agent]]` block) | Inline TOML array | **Replace with `agents/<name>/` directories** + optional `agent.toml` | Convention gap | #608 |
| `prompt_template` | Path string field | **Replace with convention:** `agents/<name>/prompt.template.md` or `prompt.md` | Convention gap | |
| `overlay_dir` | Path string field | **Replace with convention:** `agents/<name>/overlay/` or `overlays/` | Convention gap | |
| `namepool` | Path string field | **Replace with convention:** `agents/<name>/namepool.txt` | Convention gap | |
| `inject_fragments` | Present | **Remove.** Replaced by explicit `{{ template }}` in `.template.md` | V1 remnant | |
| `fallback` | Present | **Remove.** Replaced by qualified names + explicit precedence | V1 remnant | |
| `session_setup_script` | Path string | Keep, but resolve against pack root (not city) | OK (path semantics change) | |
| All other agent fields | Present | Move to `agent.toml` inside `agents/<name>/` | Placement (inline → file) | |

## AgentDefaults

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| (struct exists) | City-level only | **Both pack.toml and city.toml** | Placement mismatch | |
| `append_fragments` | Present ✓ | Present ✓ (migration bridge) | OK | |
| `allow_overlay` | Present | Not discussed in spec | Needs spec decision | |
| `allow_env_override` | Present | Not discussed in spec | Needs spec decision | |

## AgentOverride (rig overrides)

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `inject_fragments` | Present | **Remove** (V1 remnant) | V1 remnant | |
| `inject_fragments_append` | Present | **Remove** (V1 remnant) | V1 remnant | |
| `prompt_template` | Present | Spec says patches use `patches/` dir for prompt replacement | Naming/convention gap | |
| `overlay_dir` | Present | Convention-based in V2 | Convention gap | |

## AgentPatch

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `dir` + `name` targeting | Present | Spec says **target by qualified name** (`gastown.mayor`) | Naming mismatch | |
| `inject_fragments` | Present | **Remove** | V1 remnant | |
| `inject_fragments_append` | Present | **Remove** | V1 remnant | |
| `prompt_template` | Path string | Spec: `prompt = "file.md"` relative to `patches/` dir | Naming/convention gap | |
| `overlay_dir` | Present | Convention-based | Convention gap | |

## FormulasConfig

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `dir` | Present, default "formulas" | **Remove entirely.** `formulas/` is a fixed convention | V1 remnant | |

## Import

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `source` | Present ✓ | `source` ✓ | OK | |
| `version` | Present ✓ | Present ✓ | OK | |
| `export` | Present ✓ | Present ✓ | OK | |
| `transitive` | Present ✓ | Present ✓ | OK | |
| `shadow` | Present ✓ | Present ✓ | OK | |

## Rig

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| `path` | Required in city.toml | **Move to `.gc/site.toml`** — machine-local binding | Placement mismatch | #588 |
| `prefix` | In city.toml | **Move to `.gc/`** — derived, baked into bead IDs | Placement mismatch | #588 |
| `suspended` | In city.toml | **Move to `.gc/`** — operational toggle | Placement mismatch | #588 |
| `formulas_dir` | Present | **Remove.** No rig-local formula dir; use rig-scoped import instead | V1 remnant | |
| `includes` | Present | **Remove.** Replaced by `[rigs.imports]` | V1 remnant | |
| `imports` | Present ✓ | Present ✓ | OK | |
| `overrides` | Present (V1 name) | **Rename to `patches`** | Naming mismatch | |
| `patches` | Present ✓ | Present ✓ (V2 name) | OK (both accepted during migration) | |

## PackSource

| Field | As-built | Spec says | Category | Issue |
|-------|----------|-----------|----------|-------|
| (entire struct) | Present | **Remove.** V1 mechanism, replaced by `[imports]` + `pack.lock` | V1 remnant | |

## Not yet in code (spec says should exist)

| Concept | Spec location | Status |
|---------|--------------|--------|
| `pack.toml` as a separate parsed file | doc-pack-v2, doc-loader-v2 | Loader reads it but City struct doesn't separate Pack vs Deployment |
| `.gc/` SiteBinding as distinct struct | doc-loader-v2 | Not modeled as a separate input |
| `orders/` top-level convention discovery | doc-directory-conventions | Orders still under `formulas/orders/` in bundled packs (#611) |
| `commands/` convention discovery for root city pack | doc-commands | #604 |
| `patches/` directory for prompt replacements | doc-agent-v2 | Not implemented |
| `skills/` directory discovery | doc-agent-v2 | Not implemented |
| `mcp/` TOML abstraction | doc-agent-v2 | Not implemented |
| `template-fragments/` discovery | doc-agent-v2 | Partially implemented |
| `per-provider/` overlay filtering | doc-agent-v2 | Implemented on pack-v2 branch |
| `gc register --name` flag | doc-pack-v2 | #602 |
| `.gc/site.toml` for rig bindings | doc-pack-v2 | #588 (may slip post-0.13.6) |

---

## Summary by category

| Category | Count | Notes |
|----------|-------|-------|
| V1 remnant | ~15 | Fields that exist in code but spec says remove/replace |
| Placement mismatch | ~10 | Field in wrong file (city.toml vs pack.toml vs .gc/) |
| Convention gap | ~6 | Code uses TOML where spec says directory convention |
| Naming mismatch | ~3 | Field exists under different name |
| Not yet implemented | ~11 | Spec describes features not in release branch |
| OK | ~15 | Code matches spec |

## Recommended prioritization for 0.13.6

**Must fix (release-blocking):**
- `[[agent]]` → `agents/<name>/` discovery for root city pack (#609, #610)
- `gc agent add` scaffolding (#608)
- `gc init` v2 scaffold (#603 — partially done)
- Root city-pack commands (#604)
- Bundled pack order paths (#611)

**Should fix (quality):**
- `[formulas].dir` removal (accept fixed convention)
- `overrides` → `patches` rename on rigs (both accepted during migration — OK for now)
- `inject_fragments` / `global_fragments` deprecation warnings

**Defer (post-0.13.6):**
- `[workspace]` removal (#600)
- `.gc/site.toml` for rig bindings (#588)
- Full pack.toml / city.toml / .gc/ structural separation in the loader
- `skills/`, `mcp/`, `patches/` directory discovery
- `include`, `packs`, `includes` removal
