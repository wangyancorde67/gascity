---
title: "Migrating to Pack/City v.next"
description: How to move an existing Gas City city or pack to the new pack/city schema and directory conventions.
---

This guide walks through the current Pack/City v.next direction and how
to migrate existing Gas City products toward it.

It is organized in the order that migration work tends to happen:

1. split `city.toml` and `pack.toml`
2. replace includes with imports
3. migrate each area of the pack one at a time
4. use the reference section at the end to look up exact old-to-new mappings

## Before you start

The important mental shift is:

- **V1** centers `city.toml` and uses explicit path wiring
- **V2** centers `pack.toml`, named imports, and convention-based directories

V2 separates three concerns:

- **pack definition**
  - portable, shareable definition of agents, formulas, orders, commands,
    doctor checks, overlays, and related assets
- **city deployment**
  - team-shared deployment decisions about rigs, capacity, and runtime policy
- **site binding**
  - machine-local state and runtime data in `.gc/`

## First: split `city.toml` and `pack.toml`

This is the most important migration step. Everything else hangs off it.

### What belongs in `pack.toml`

`pack.toml` answers:

- what this pack is
- what it imports
- what defaults and pack-wide policy it establishes

Today, the current direction is that `pack.toml` should contain things like:

- `[pack]` metadata
- `[imports.*]`
- `[providers.*]`
- `[agents]` defaults
- `[[named_session]]`
- pack-level patches

See [Reference: `pack.toml` contents](#reference-packtoml-contents) for
the fuller list.

### What belongs in `city.toml`

`city.toml` answers:

- how this city is deployed

It should hold deployment-specific things such as:

- rigs
- capacity
- deployment-oriented runtime policy

It should no longer be the home for the pack's portable definition.

### What belongs in `.gc/`

`.gc/` is site binding and runtime state:

- path bindings
- prefixes
- caches
- sockets
- logs
- runtime state

### The first concrete change: replace includes with imports

If you are migrating an existing city, this is often the first schema
change you actually feel.

V1 composition is include-oriented. V2 composition is import-oriented.

Move pack composition toward `pack.toml`:

```toml
[imports.gastown]
source = "https://github.com/example/gastown"
version = "^1.2"
```

The binding name, here `gastown`, becomes the local name used to refer
to imported content inside this city or pack.

That is the key shift:

- pack composition moves out of `city.toml`
- imports live in `pack.toml`
- imported content gets a stable local name

### A practical migration order for cities

For an existing city, the clean order is:

1. create or promote a root `pack.toml`
2. move pack includes to `[imports.*]` in `pack.toml`
3. move portable definition out of `city.toml`
4. leave only deployment in `city.toml`
5. leave only site binding and runtime state in `.gc/`

After that, the rest of the work becomes area-by-area restructuring.

## Then: migrate area by area

Once the root split is in place, migration becomes much easier to reason
about one surface at a time.

## Agents

### Direction

Agents move from inline TOML definitions toward agent directories.

Old shape:

```toml
[[agent]]
name = "mayor"
prompt_template = "prompts/mayor.md"
overlay_dir = "overlays/default"
```

New direction:

```text
agents/
└── mayor/
    ├── prompt.md
    └── agent.toml
```

Use `agent.toml` only when the agent needs overrides beyond shared
defaults from `[agents]`.

### What moves where

- agent identity and prompt move into `agents/<name>/`
- pack-wide defaults stay in `[agents]`
- pack-wide providers stay in `[providers.*]`

If you are migrating a city, the same rule applies: city-local agents
are still just agents in the root city pack.

## Formulas

### Direction

Formulas already mostly fit the new model.

Current direction:

```text
formulas/
└── build-review.formula.toml
```

The main change is to treat `formulas/` as a stronger fixed convention
rather than something that needs directory wiring.

### What to check

- move toward fixed `formulas/`
- stop treating formula location as something that needs extra path config
- move any nested orders out of formula space and into `orders/`

## Orders

Yes: the current local design direction is that orders should move to
look more like formulas.

The strongest written guidance for that is in the consistency audit:

- move orders out of `formulas/orders/`
- standardize on top-level `orders/`
- adopt flat files like `orders/<name>.order.toml`

### Why

Orders are not formulas.

- formulas define workflow structure
- orders reference formulas and schedule or gate dispatch

So the old nesting:

```text
formulas/
└── orders/
    └── nightly-sync/
        └── order.toml
```

is the wrong conceptual shape.

### New direction

```text
orders/
└── nightly-sync.order.toml
```

This gives you a more consistent pair:

- `formulas/<name>.formula.toml`
- `orders/<name>.order.toml`

### Migration step

If a city or pack currently uses `formulas/orders/...`, move those
definitions to top-level `orders/` as flat files.

## Commands

### Direction

Commands are moving toward convention-first entry directories.

Simple case:

```text
commands/
└── status/
    └── run.sh
```

This should be enough for a default single-word command.

Richer case:

```text
commands/
└── repo-sync/
    ├── command.toml
    ├── run.sh
    └── help.md
```

Use `command.toml` only when the default directory-name mapping is not
enough, for example:

- multi-word command placement
- extension-root placement
- description or richer metadata
- non-default entrypoint

### Migration step

Old:

```toml
[[commands]]
name = "status"
description = "Show status"
script = "commands/status.sh"
```

New simple case:

```text
commands/status/run.sh
```

New richer case:

```text
commands/repo-sync/
├── command.toml
├── run.sh
└── help.md
```

## Doctor checks

Doctor checks are moving in parallel with commands.

Simple case:

```text
doctor/
└── binaries/
    └── run.sh
```

Richer case:

```text
doctor/
└── git-clean/
    ├── doctor.toml
    ├── run.sh
    └── help.md
```

The migration rule is the same as commands:

- keep the entrypoint local to the check that uses it
- use local TOML only when the default mapping is not enough

## Overlays

Overlays move away from being a path-wired global bucket and toward a
clear split between pack-wide and agent-local content.

Use:

- `overlays/` for pack-wide overlay material
- `agents/<name>/overlay/` for agent-local overlay material

If your old config depends on `overlay_dir = "..."`, the migration move
is usually to relocate those files into one of those two places.

## Skills, MCP, and template fragments

These mostly follow the new directory structure directly.

Use:

- `skills/` for pack-wide skills
- `mcp/` for pack-wide MCP assets
- `template-fragments/` for pack-wide prompt fragments

and:

- `agents/<name>/skills/`
- `agents/<name>/mcp/`
- `agents/<name>/template-fragments/`

when the asset belongs to one specific agent.

## Assets and paths

This is the positive rule that replaces a lot of V1 ad hoc path habits.

### `assets/` is the opaque home

If a file is not part of a standard discovered surface, it belongs in
`assets/`.

Examples:

- helper scripts
- static data files
- fixtures and test data
- imported pack payloads carried inside another pack

### Path-valued fields

Any field that accepts a path may point to any file inside the same
pack.

That includes:

- files under standard directories
- files under `assets/`
- relative paths that use `..`

The hard constraint is:

- after normalization, the path must still stay inside the pack root

### Good examples

```toml
run = "./run.sh"
help = "./help.md"
run = "../shared/run.sh"
source = "./assets/imports/maintenance"
```

if they stay inside the pack.

### Caution

One open design question is how freely we want path-valued fields to
reach into other structured directories such as agent directories. The
guide assumes "any path inside the pack is allowed" because that is the
broad current direction, but that boundary may still get sharpened in
implementation.

## Migrating a reusable pack

Everything above applies to reusable packs just as much as to cities.

The difference is only that a reusable pack does not have:

- `city.toml`
- `.gc/`

Otherwise the migration work is the same:

1. narrow `pack.toml`
2. move definition into standard directories
3. move opaque helpers into `assets/`
4. migrate commands, doctor checks, and orders to their new shapes

## Common migration gotchas

### "I still have a lot in `city.toml`"

That usually means definition and deployment are still mixed together.

Ask:

- is this portable definition?
- is this team-shared deployment?
- is this machine-local state?

Then move it to:

- `pack.toml` and pack directories
- `city.toml`
- `.gc/`

respectively.

### "I used to rely on `scripts/`"

Do not recreate `scripts/` as a top-level convention just because V1 had
it.

Instead:

- put entrypoint scripts next to the command or doctor entry that uses them
- put general opaque helpers under `assets/`

### "Do I need TOML everywhere?"

No. That is one of the design tests for the new model.

Simple cases should work by convention:

- `agents/<name>/prompt.md`
- `commands/<name>/run.sh`
- `doctor/<name>/run.sh`

TOML should appear when it is actually needed for:

- defaults
- overrides
- metadata
- explicit placement
- compatibility or policy

## Reference: current to new

This section is the quick lookup table for converting existing content.

### Root files

| Current | New direction |
|---|---|
| `city.toml` holds almost everything | Split into `pack.toml` + `city.toml` + `.gc/` |
| `pack.toml` acts as both metadata and inventory | `pack.toml` becomes pack-wide declarative policy |

### TOML elements

| Current element | New direction |
|---|---|
| `[[agent]]` in `pack.toml` or `city.toml` | `agents/<name>/` with optional `agent.toml` |
| `prompt_template = "..."` | `agents/<name>/prompt.md` |
| `overlay_dir = "..."` | `overlays/` or `agents/<name>/overlay/` |
| `scripts_dir = "scripts"` | no standard `scripts/`; use colocated scripts or `assets/` |
| `[[commands]]` | `commands/<name>/run.sh` by default, optional `command.toml` |
| `[[doctor]]` | `doctor/<name>/run.sh` by default, optional `doctor.toml` |
| `workspace.includes` / `rig.includes` | `[imports.*]` in `pack.toml` |
| `[formulas].dir` | fixed `formulas/` convention |
| `formulas/orders/<name>/order.toml` | `orders/<name>.order.toml` |

### Directory structure

| Current | New direction |
|---|---|
| `prompts/` as a global bucket | prompts live with the agent that owns them |
| `scripts/` as a global bucket | use colocated entrypoint scripts or `assets/` |
| `formulas/` | stays, but as a stronger fixed convention |
| `formulas/orders/` | move to top-level `orders/` |
| loose opaque files at the root | put opaque pack-owned files under `assets/` |

### Reference: `pack.toml` contents

| Keep in `pack.toml` | Move out of `pack.toml` |
|---|---|
| `[pack]` metadata | individual agent definitions |
| `[imports.*]` | prompt paths |
| `[providers.*]` | overlay path wiring |
| `[agents]` defaults | script directory wiring |
| `[[named_session]]` | simple command inventory |
| patches and pack-wide policy | simple doctor inventory |

## Suggested migration order

For a real city or pack, the most practical order is:

1. split `city.toml` and `pack.toml`
2. replace includes with imports in `pack.toml`
3. move agents into `agents/`
4. move orders to top-level flat files
5. move commands and doctor checks into `commands/` and `doctor/`
6. move opaque helpers into `assets/`
7. finish the remaining pack-wide cleanup in `pack.toml`

That gets the big structural changes done before you spend time on the
smaller cleanup work.
