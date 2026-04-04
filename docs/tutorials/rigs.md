# Rigs

The city chapter covers the basics of rigs — adding them with `gc rig add`, listing, suspending, resuming, and removing. The agents chapter covers rig-scoped agents, rig overrides, and `default_sling_target`.

This chapter collects the remaining rig details for reference.

## The rigs entry in city.toml

The full set of fields on a `[[rigs]]` entry:

```toml
[[rigs]]
name = "my-app"
path = "/path/to/my-app"
includes = ["packs/gastown"]
prefix = "ma"                           # Bead ID prefix (auto-derived if omitted)
default_sling_target = "my-app/polecat" # Default agent for gc sling
suspended = false                       # Suspend all agents in this rig
max_active_sessions = 10                # Rig-wide session cap
```

## Rig status

For detailed information on a specific rig:

```
$ gc rig status my-app
Rig: my-app
Path: /path/to/my-app
Suspended: no

Agents:
  my-app/witness     running
  my-app/refinery    stopped
  my-app/polecat     pool (0/5 active)
```

## Restarting

If you need to force-refresh all agents in a rig:

```
$ gc rig restart my-app
Restarted rig 'my-app' (3 sessions killed, reconciler will restart)
```

This kills all running sessions for the rig. The controller restarts them automatically on its next tick.

## Command reference

| Command | What it does |
|---|---|
| `gc rig add <path>` | Register a project directory as a rig |
| `gc rig add <path> --name <name>` | (*with explicit name*) |
| `gc rig add <path> --include <pack>` | (*with pack includes*) |
| `gc rig list` | List registered rigs |
| `gc rig status <name>` | Show rig details and agent status |
| `gc rig suspend <name>` | Suspend all agents in a rig |
| `gc rig resume <name>` | Resume a suspended rig |
| `gc rig restart <name>` | Kill and restart all rig sessions |
| `gc rig remove <name>` | Unregister a rig |
| `gc rig default <name>` | Set the default rig |

<!--
BONEYARD — draft material for future sections. Not part of the published tutorial.

### Multi-city rigs

A single project directory can be registered as a rig in multiple cities. When this happens, you need to set a default city for the rig:

```
$ gc rig default my-app --city gastown-dev
```

This controls which city's hooks and routing files are active in the rig directory.

### Rig-local formulas

Rigs can have their own formula directory that layers on top of city and pack formulas:

```toml
[[rigs]]
name = "my-app"
path = "/path/to/my-app"
formulas_dir = "formulas"
```

Rig-local formulas shadow city formulas with the same name — the layered resolution system checks rig, then city, then packs.

### Session sleep per rig

Rigs can override the city-wide session sleep policy:

```toml
[[rigs]]
name = "my-app"
path = "/path/to/my-app"

[rigs.session_sleep]
enabled = true
after = "15m"
```

### Start suspended

You can add a rig in suspended mode so it doesn't immediately start agents:

```
$ gc rig add /path/to/my-app --start-suspended
```

Useful when you want to configure overrides before agents spin up.
-->
