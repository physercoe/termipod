# Steward templates

> **Type:** reference
> **Status:** Current (2026-05-01)
> **Audience:** contributors · template authors · operators
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Steward template authoring contract. Two tiers — **frozen** general steward (one bundled file, hub-release-only edits) and **overlay** domain stewards (per-team filesystem, editable by the director and authored by the general steward). This file documents what the templates contain, where they live, what overlay can and can't shadow, and which engine selection is wired where. Read [ADR-017](../decisions/017-layered-stewards.md) first for the *why*; this file is the *what* and *where*.

---

## File layout

```
hub/templates/                            # bundled into the hub binary via embed.FS
├── agents/
│   ├── steward.general.v1.yaml           # FROZEN — see "Frozen surfaces" below
│   ├── steward.research.v1.yaml          # bundled seed (overlay-replaceable)
│   ├── steward.infra.v1.yaml             # bundled seed
│   ├── steward.briefing.v1.yaml          # bundled seed (named per project, e.g. steward.briefing-east)
│   ├── steward.codex.v1.yaml             # bundled seed (codex engine)
│   ├── steward.gemini.v1.yaml            # bundled seed (gemini-cli engine)
│   ├── steward.v1.yaml                   # legacy plain steward (single-steward installs)
│   ├── ml-worker.v1.yaml                 # worker
│   ├── lit-reviewer.v1.yaml              # worker
│   ├── coder.v1.yaml                     # worker
│   ├── critic.v1.yaml                    # worker
│   └── paper-writer.v1.yaml              # worker
└── prompts/
    └── *.md                              # one prompt file per template (same basename)

<DataRoot>/teams/<team>/templates/        # overlay (per-team, runtime-editable)
├── agents/
│   └── steward.<domain>.v1.yaml          # overrides bundled seed if same name
└── prompts/
    └── steward.<domain>.v1.md
```

**Resolution rule.** When the hub loads a template by name, it checks the team overlay first; if not present, falls back to the bundled file. **Exception:** `steward.general.v1` is **never** read from overlay — the bundled file always wins (D7 / frozen invariant).

---

## Template anatomy

A steward template (YAML) has four required sections plus a backend block:

```yaml
template: steward.research.v1            # canonical id (matches filename)
kind: claude-code                         # engine kind; one of claude-code | codex | gemini-cli
role: steward                             # roles.yaml key (always 'steward' for any *.steward.* file)
prompt: steward.research.v1.md            # basename of prompts/*.md to embed at spawn

backend:
  kind: claude-code                       # repeated here for backward-compat readers
  model: claude-opus-4-7                  # optional; defaults to engine's CLI default
  permission_mode: prompt                 # prompt | dangerously-skip | default
  flags: []                               # extra CLI flags

defaults:
  workdir: ~/hub-worker                   # spawn cwd on the host
  env: {}                                 # extra env vars
```

**Template id naming.** `steward.<domain>.v1` for new ones (the `.v1` is a placeholder for future format-version bumps; we have not had to bump it). The general steward is `steward.general.v1` — the `.general` namespace is reserved.

**Engine kind selection** (`backend.kind`) is the load-bearing field for the spawn-steward sheet's UX (`lib/screens/team/spawn_steward_sheet.dart`) — choosing a template should display "Codex" / "Gemini CLI" / "Claude Code" info text matching the template, not a hardcoded default. See [bug fix in v1.0.350-alpha](https://github.com/physercoe/termipod/commit/8d04851).

---

## Prompt files

Each template has a sibling prompt at `hub/templates/prompts/<name>.md`. The prompt is loaded once at spawn and embedded as the agent's system message. Convention:

- **No model-specific tokenising.** Prompts are plain markdown; the engine driver wraps them appropriately.
- **One concept per section.** H2 per concept (Role, How you work, Constraints, Tools, Examples). Easier for the agent to reason about.
- **No secrets.** Prompts ship in the binary; treat them as public.

The general steward's prompt is the longest because it carries the full concierge framing + manager/IC invariant + bootstrap protocol. Other domain stewards inherit principles from it implicitly (the general steward authored their prompts) but the prompt itself is local to each template.

---

## Frozen surfaces (read-only at runtime)

**Frozen** means: hub never writes to the file at runtime; the director cannot edit it via the mobile editor; the agent itself cannot edit it (ADR-016 D7 self-modification guard); changing it requires a hub release.

| File | Why frozen |
|---|---|
| `hub/templates/agents/steward.general.v1.yaml` | The general steward has the broadest role coverage in the system. The director's only structural assurance about its behaviour is the bundled prompt + ADR-016 D6 role gating. Editable prompt = erodable assurance. |
| `hub/templates/prompts/steward.general.v1.md` | Same. |

Every other template under `hub/templates/` is bundled as a **seed**, not frozen. The general steward copies seeds to overlay on first project create; from that point the director edits the overlay, and the bundled seed is just the fallback when no overlay exists yet.

---

## Authoring rules

### What the general steward authors

- New domain steward templates (`steward.<domain>.v1.yaml` + matching prompt). Written to overlay. Replicates the seed structure with domain-specific prompt.
- Worker templates (`<worker>.v1.yaml`). Same pattern.
- Plan templates (`<plan>.v1.yaml`) under `hub/templates/plans/` (if extending overlay to plans; current hub bundles plans only — plan overlay is OQ).

### What the director authors

- Direct edits to overlay templates via the mobile template editor (`lib/screens/team/templates_screen.dart`).
- Plan templates if the hub adds plan overlay support.

### What no one authors at runtime

- `steward.general.v1` (frozen).
- The bundled seed files themselves (those are checked-in source; runtime edits would be lost on hub upgrade).

### Conflict resolution (overlay vs. bundled vs. concurrent edit)

- Overlay always wins for non-frozen templates.
- Concurrent edits (general steward authoring + director editing the same overlay file) are last-write-wins. There is no editor lock or merge UI — frequency is too low to invest in. If a conflict happens in practice, escalate to a wedge.

---

## Spawn-time validation

When spawning a steward (general or domain), the hub validates:

1. The template exists (overlay-then-bundled lookup).
2. `backend.kind` is one of the three supported engine kinds (`claude-code`, `codex`, `gemini-cli`). New engines need a frame profile (ADR-010) before they can land here.
3. `role` matches `roles.yaml` (ADR-016 D6) — `steward` for steward.* files; `worker` for worker files.
4. The handle (D2 in ADR-017) is unique within the team for stewards (live-uniqueness check).

Spawn fails with a 4xx error if any check fails; the mobile spawn sheet surfaces the message.

---

## Engine kind ↔ template mapping

| Template | Default `backend.kind` | Engine driver |
|---|---|---|
| `steward.general.v1` | `claude-code` | `hub/internal/hostrunner/driver_claude.go` |
| `steward.research.v1` | `claude-code` | same |
| `steward.codex.v1` | `codex` | `hub/internal/hostrunner/driver_codex.go` (ADR-012) |
| `steward.gemini.v1` | `gemini-cli` | `hub/internal/hostrunner/driver_gemini.go` (ADR-013) |
| `ml-worker.v1` | `claude-code` | claude default |
| `lit-reviewer.v1` | `claude-code` | claude default |
| `paper-writer.v1` | `claude-code` | claude default |
| `coder.v1` | `claude-code` | claude default |
| `critic.v1` | `claude-code` | claude default |

Overlay edits to `backend.kind` switch the engine driver immediately on next spawn — a research steward can be migrated from claude to codex by editing the template's `backend.kind`. Frame profiles (ADR-010) keep the transcript renderer compatible.

---

## References

- [ADR-017](../decisions/017-layered-stewards.md) — the design (read first).
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — D6 role gate, D7 self-mod guard.
- [ADR-010](../decisions/010-frame-profiles-as-data.md) — frame profile model that lets new engines land without code changes.
- [ADR-012](../decisions/012-codex-app-server-integration.md), [ADR-013](../decisions/013-gemini-exec-per-turn.md) — engine driver contracts referenced by `backend.kind`.
- Code: `hub/templates/`, `hub/internal/server/handlers_general_steward.go`, `lib/screens/team/templates_screen.dart`, `lib/screens/team/spawn_steward_sheet.dart`.
