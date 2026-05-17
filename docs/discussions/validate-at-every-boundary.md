---
name: Validate at every boundary
description: Lesson captured from the coder.v1 spawn incident — a steward passed `spawn_spec_yaml: "template: coder.v1"` in good faith, the hub passed it through unchecked, the hostrunner fell through to a bash placeholder, PaneDriver pumped the task prompt as keystrokes, and the agent spent ~20 minutes in a respawn loop creating tmux windows. Every layer was individually permissive; the failure was the absence of validation at every boundary. The discussion lays out the principle (every boundary validates; no layer trusts the next to catch its mistakes), audits where termipod has the gap (7 HIGH-severity free-form fields with no shape validation), and proposes the four-layer defensive strategy shipping in v1.0.620.
---

# Validate at every boundary

> **Type:** discussion
> **Status:** Open (2026-05-17) — captured the day of the coder.v1
> incident. Most of the structural fixes ship in v1.0.620 (see
> [plan](../plans/spawn-robustness-and-validators.md)). This doc is the
> durable lesson so future contributors don't relitigate the principle.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.619-alpha

**TL;DR.** A boundary between two components is a contract. Today's
hub trusts callers to send valid payloads, trusts templates to be
self-consistent, trusts host-runners to refuse malformed specs, and
trusts the launcher's bash placeholder to never actually be reached.
Every one of those trusts is *vibes* — no code enforces them. When
a steward sent the well-formed-but-meaningless spec `template:
coder.v1` in good faith, every layer's permissive default cascaded
into a 20-minute respawn loop with the principal manually closing
tmux windows. The principle: **every layer must validate the
inputs it accepts and refuse what it can't safely execute, even
when the next layer "would catch it."** This doc captures the
incident as case study, names the four defensive layers
(handler-typed-decode / startup-time bundled audit / CI lint /
description-shape audit), audits termipod's current gap (7
HIGH-severity free-form fields), and points at the v1.0.620 bundle
that closes it.

---

## 1. The incident as case study

A project steward on Claude Code spawned a worker:

```json
{
  "child_handle": "summarizer-2",
  "host_id": "...",
  "kind": "claude-code",
  "project_id": "...",
  "spawn_spec_yaml": "template: coder.v1",
  "task": { ... }
}
```

The MCP description never said what `spawn_spec_yaml` should
contain. The steward, acting on the template-shorthand convention
seen elsewhere (`templates_propose`, `project_templates`),
constructed `template: coder.v1` and submitted.

What happened, layer by layer:

| # | Layer | Behaviour | Should have done |
|---|---|---|---|
| 1 | Hub `agents.spawn` MCP wrapper (`tools.go:412`) | Validated presence of `child_handle`, `kind`, `spawn_spec_yaml` as non-empty strings. Passed through. | Decoded the YAML and validated `backend.cmd` is non-empty after template merge. |
| 2 | Hub `DoSpawn` (`handlers_agents.go:822`) | Inserted agent row (status='pending') + spawn record. Returned `{status: "spawned"}`. | Validated rendered spec has the fields the host-runner needs. Rejected with 422 if not. |
| 3 | Hub `renderSpawnSpec` (`template.go:164`) | Saw no `{{var}}` placeholders in `"template: coder.v1"`. Returned unchanged. | Recognised `template:` as a reference, loaded `coder.v1.yaml`, merged its `backend.{cmd,kind,model,permission_modes}` and persona file into the spec. |
| 4 | Hostrunner M4 LocalLogTail (`launch_m4_locallogtail.go:196`) | Hard-failed: "backend.cmd is empty". Returned error. | Same — this layer DID validate. (Only layer that did.) |
| 5 | Hostrunner M4 PaneDriver fallback (`runner.go:618`) | Tried `templates.BackendCmd(sp.Kind)` — wrong key (engine kind, not template name). Got `""`. Fell through to launcher default. | Refused to launch a placeholder; marked agent `status='failed'` with structured reason. |
| 6 | TmuxLauncher default (`tmux_launcher.go:32`) | Ran `bash -c '... exec bash'` — interactive bash shell in the pane. | Either no placeholder at all, or `exit 1` so the pane terminates instead of staying as a bash prompt. |
| 7 | PaneDriver `Start()` | Pumped the rendered task prompt via `tmux send-keys`. Bash interpreted it as commands: `tasks.complete: command not found`, `BOUNDARIES:: command not found`. | Detected the pane was a bash shell (no engine handshake) and refused to inject input. |
| 8 | Reconciler (`runner.go:653`) | Status never flipped `pending → running` (foreground was a shell). Spawn stayed `pending`. | Same; reconciler can't tell the difference, which is *why* upstream layers must validate. |
| 9 | TickPoll loop (`runner.go:441`) | Re-saw the pending spawn every tick. No dedup against `a.drivers[ChildID]`. Re-launched. New tmux window. Repeat. | Skipped agents already in the local `drivers` map. |
| 10 | Principal | Manually closed tmux windows for 20 minutes until enough state corruption forced a clean termination. | Should never have been the validator of last resort. |

**Ten layers. One ("locallogtail M4: backend.cmd is empty") refused
to act on bad input. Nine deferred to "someone else will catch
it." Nobody did until layer 10 — a human.**

---

## 2. The principle

> **Every boundary is a validator. No layer trusts the next layer
> to catch its own mistakes.**

Boundaries that matter in termipod:

- MCP verb call (steward → hub)
- HTTP request (hub external API)
- Database write (any handler → SQLite)
- Hub → host-runner spawn lookup (polling)
- Host-runner → launcher (process exec)
- Driver → pane (`tmux send-keys` injection)
- Driver → engine (stream-json / JSON-RPC frame send)
- A2A relay (agent → agent, via hub)
- Template load (hostrunner / hub startup)

At each one, the inbound side has a contract about what it accepts.
Today, most of those contracts are *implicit* — "callers are
expected to be well-behaved." Implicit contracts are unenforced
contracts. They break the first time a caller acts in good faith
on incomplete documentation, which is exactly what stewards do
every day.

### Three reasons this principle is non-negotiable

1. **Agents are stochastic.** Per [blueprint A3](../spine/blueprint.md#2-design-philosophy-three-axioms),
   every autonomous action must be bounded by a rule that existed
   before the action happened. "I trusted the LLM to construct a
   valid spec" is not a rule.
2. **Stewards act on imperfect documentation.** Documentation will
   always lag behind code. The validator IS the contract; the
   description merely explains what the validator enforces.
3. **Silent fallbacks cascade.** Each layer's "I'll be lenient and
   pass it on" multiplies with the next. Ten lenient layers
   compound into a 20-minute incident; one fail-fast layer at the
   top prevents the cascade entirely.

### Two corollaries

- **Liberal in what you accept, strict in what you commit.** A
  parser can be forgiving of whitespace, optional fields, alias
  names. A *validator* must be strict about what the system can
  load-bear on. The two roles are not the same code.
- **Refusal beats coercion.** When a layer can't safely execute
  what it received, the right answer is HTTP 422 / structured
  error / `status='failed'` — never "I'll do something
  approximately like what was asked." The launcher's bash
  placeholder is the canonical example of "approximately like
  what was asked, with no surface telling anyone."

---

## 3. The four defensive layers (and where termipod has them)

For any free-form field (YAML blob, JSON object, markdown body,
URI template, glob), four validators close the gap. They compose:
each catches a different class of mistake.

| # | Validator | Catches | termipod today |
|---|---|---|---|
| 1 | **Typed handler decode + required-field check** | Caller sends a malformed payload | Partial — some handlers decode into typed structs and check required strings; none decode the free-form YAML/JSON fields inside them |
| 2 | **Startup-time bundled-resource audit** | Author shipped a broken template / config / policy file | **None.** `loadAgentTemplates` deliberately silent-skips broken templates so one bad file doesn't take launch offline. Defensible for parse errors; insufficient for semantic errors (parses but has no `backend.cmd`) |
| 3 | **CI lint** | Drift between code and bundled resources lands in main | **None for templates/policies.** We have `lint-docs.sh` / `lint-glossary.sh` / `lint-openapi.sh`. Nothing checks that every bundled template can produce a launchable spec |
| 4 | **Description ↔ schema audit** | Tool description claims a field accepts X but the schema/validator allows Y | **None.** MCP `InputSchema` and `Description` are two strings authored by hand with no consistency check |

The audit of all 33 MCP tools + 15 template tools found **7
HIGH-severity gaps** in layer 1 — fields where the caller can send
a structurally well-formed but semantically empty payload and the
handler will accept it:

| Tool | Field | Failure mode if empty/wrong |
|---|---|---|
| `agents.spawn` | `spawn_spec_yaml` | bash placeholder + respawn loop (the incident) |
| `plans.steps.create` | `spec_json` | step inserted but executor doesn't know what to do — silently stalled plan |
| `projects.create` | `config_yaml` | project created with no phases/criteria — empty shell |
| `documents.create` | `body` | empty doc |
| `channels.post_event` | `parts` | event with no content |
| `runs.attach_artifact` / `artifacts.create` | `lineage_json` | lineage gap |
| `projects.update` | `policy_overrides_json` | policy update no-ops |

Plus 6 MEDIUM and 5 LOW-severity gaps that bite less hard. The
common shape: free-form field with no shape documentation and no
runtime validator.

---

## 4. What ships in v1.0.620

[`docs/plans/spawn-robustness-and-validators.md`](../plans/spawn-robustness-and-validators.md)
covers the 10-wedge bundle. Mapping to the four-layer strategy:

| Layer | Wedges in v1.0.620 |
|---|---|
| 1. Typed handler decode | W1 (template merge in `renderSpawnSpec`), W4 (hub fail-fast on empty `backend.cmd`), W9 (sync-wait three-state return), W10 (typed validators for the 7 HIGH-severity fields) |
| 2. Startup-time bundled audit | W10 (loops every bundled template, renders, validates, fails hub start if any template is broken) |
| 3. CI lint | W10 (`lint-templates.sh` mirrors the startup audit; runs in PRs) |
| 4. Description ↔ schema audit | W6 (rewrite 7 HIGH-severity tool descriptions with field shape + minimal example + silent-failure warnings; future ADR may add an automated schema-vs-description lint) |

Plus the supporting structural fixes:
- W2 (hostrunner template-index key mismatch — defused by W1)
- W3 (`launchOne` dedup, prevents respawn loop independent of validator)
- W5 (`task.notify` triggers steward turn, unrelated to validation but surfaced by the same incident)
- W7 (hostrunner refuse-to-launch with `status='failed'`, layer 5/6 from §1)
- W8 (harden launcher default placeholder, layer 6 from §1)

**Cost: ~840 LOC + ~580 lines prose, single ship → v1.0.620.**

---

## 5. What this is not

- **Not a call for "validate everything always."** Validators have
  cost — code, test surface, false-positive risk, version-drift
  burden. Apply them where the layer can't safely execute the
  payload, not where it merely *prefers* a particular shape.
- **Not "schema-validate every MCP tool with a JSON Schema
  library."** We do not import a schema-validation dependency for
  this MVP. Typed Go decode + per-kind validators is simpler,
  matches existing patterns, and avoids the schema-library
  lock-in. A future ADR may revisit if the validator count grows
  past ~20.
- **Not "no permissive parsers anywhere."** Liberal parse +
  strict validate is the canonical pattern. We're being strict
  about what the next layer can act on, not about what the
  current layer can accept. (Example: template loader can keep
  silent-skipping unparseable files; what changes is that *parsed
  but invalid* templates now fail the startup audit.)
- **Not a forbidden-pattern entry yet.** When v1.0.620 ships and
  the four validators are proven across the 7 HIGH-severity
  fields, a new entry in [`spine/forbidden-patterns.md`](../spine/forbidden-patterns.md)
  is a candidate — something like *"Free-form payload field
  accepted at a boundary without typed validation."* That's a
  follow-up after the bundle proves the pattern works.

---

## 6. What this is

A durable lesson and a checklist for future contributors:

> When you add a new MCP tool, a new HTTP endpoint, a new bundled
> template, a new policy file shape, a new free-form payload
> field — ask:
>
> 1. What's the typed shape this field must have for the system
>    to safely act on it?
> 2. What validator runs at request time to refuse malformed
>    payloads?
> 3. What validator runs at startup to refuse malformed bundled
>    resources?
> 4. What CI lint catches drift in PRs?
> 5. What's the description-level guidance + minimal example so
>    callers don't have to read source?
>
> If any of 1-5 is "none" or "trust the next layer," reconsider.

---

## 7. Status

Open. The 10-wedge bundle is the immediate response. Once it
ships and the four-layer pattern is proven, a follow-up wedge can
add the forbidden-patterns.md entry. The discussion stays open as
the durable rationale; new boundaries gained through future ADRs
should inherit this principle without re-deriving it.

Resolves into either:
- A follow-up ADR or forbidden-pattern entry codifying the
  "every boundary validates" rule (after v1.0.620 ships +
  shakedown), OR
- A "deferred indefinitely" status if MEDIUM/LOW gaps don't
  re-surface and the v1.0.620 bundle is sufficient on its own.

---

## 8. References

- [Plan — v1.0.620 spawn robustness + validators bundle](../plans/spawn-robustness-and-validators.md)
- Foundational axiom: [spine/blueprint.md A3](../spine/blueprint.md#2-design-philosophy-three-axioms)
- Forbidden patterns: [spine/forbidden-patterns.md](../spine/forbidden-patterns.md) — candidate entry post-v1.0.620
- Related references:
  [reference/permission-model.md](../reference/permission-model.md),
  [reference/tool-call-approval-patterns.md](../reference/tool-call-approval-patterns.md),
  [reference/attention-kinds.md](../reference/attention-kinds.md)
- Related ADRs:
  [ADR-027](../decisions/027-local-log-tail-driver.md) (where M4 LocalLogTail's strict
  validation lives — the one layer that fail-fasted),
  [ADR-029](../decisions/029-tasks-as-first-class-primitive.md) (worker delivery via task body inlined into agent-memory file
  — the prompt content that ended up keystroked into bash),
  [ADR-030](../decisions/030-governed-actions-and-propose-verb.md)
  (governed actions discipline — propose verb is the next surface
  to apply the four-layer pattern to).
