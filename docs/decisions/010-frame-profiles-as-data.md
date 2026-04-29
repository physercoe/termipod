# 010. Frame profiles as data — vendor schemas leave Go for YAML

> **Type:** decision
> **Status:** Accepted (2026-04-29)
> **Audience:** contributors
> **Last verified vs code:** v1.0.347

**TL;DR.** Move every claude-code / codex / gemini-cli stream-json
field path out of `driver_stdio.go` and into per-engine *frame
profiles* embedded in `agent_families.yaml`. Each rule describes a
matcher and an output-event shape using a small hand-rolled
expression subset (no third-party DSL runtime). Existing overlay +
hot-reload infrastructure carries the profiles, so an SDK shape
change is a YAML edit + agent restart, not a host-runner rebuild.

## Context

`discussions/multi-engine-frame-parsing.md` audited the rabbit hole:
six months of `translate()` accumulating per-vendor field lifts and
casing fallbacks, with two of the last three claude-code SDK
releases requiring host-runner code changes (v1.0.326 and v1.0.328
both shipped fixes for `rate_limit_event` shape drift). Today's
plurality is one engine; the roadmap brings codex, gemini-cli, and
aider as first-class. Each speaks a different stream-json dialect of
the same conceptual surface (assistant text, tool calls, usage,
rate limits).

The blueprint (`spine/blueprint.md` §5.3.2) commits to "new agent
families land as drop-in declarations." The launch contract honors
that today (`agent_families.yaml` carries `bin` / `version_flag` /
`supports` / `incompatibilities` with overlay + hot-reload). The
parse contract silently violates it — every new field is a Go
diff.

Four options were considered (see the discussion doc §3 for full
analysis):
- **A. Declarative profiles in YAML, pure data.**
- **B. Translator subprocess per engine, JSON-RPC over stdio.**
- **C. Embedded scripting (Lua / Starlark).**
- **D. Keep hardcoded but factor cleanly.**

A is the only option that solves the user's stated pain ("hot
reload, don't recompile") without dragging in a process boundary
(B), a scripting runtime (C), or accepting the rabbit hole (D).

## Decision

Adopt Option A: per-engine **frame profiles** as data, with the
following commitments.

**D1. Profiles live inline in `agent_families.yaml`.**

Each `Family` entry gains a `frame_profile` block. One file per
engine; the launch contract (`bin`, `supports`, …) and the parse
contract (`frame_profile`) sit side-by-side because they're
authored together when adding a new engine.

```yaml
- family: claude-code
  bin: claude
  version_flag: --version
  supports: [M1, M2, M4]
  frame_profile:
    profile_version: 1
    rules:
      - match: { type: system, subtype: init }
        emit:
          kind: session.init
          producer: agent
          payload:
            session_id: "$.session_id"
            model: "$.model"
            permission_mode: "$.permissionMode || $.permission_mode"
            # …
      - match: { type: rate_limit_event }
        emit:
          kind: rate_limit
          payload:
            window: "$.rate_limit_info.rateLimitType || $.rateLimitType"
            # …
```

Overlay + hot-reload follow the existing `<DataRoot>/agent_families/<family>.yaml`
path; an operator drops a file, the loader picks it up via
`Invalidate()`, the next-spawned agent uses the new profile.

**D2. Expression language is a hand-rolled subset, not JSONata.**

The expression vocabulary supported in v1:

- **Path access** — `$.a.b.c` walks dotted keys; missing keys return nil.
- **Array element** — `$.a.b[0]` pulls indexed items.
- **Coalesce** — `$.a || $.b || "literal"` returns the first non-nil
  value; trailing string literal acts as a default.
- **Outer scope** — `$$.message.id` references the parent frame
  during `for_each` walks (the inner walk variable is `$.`).
- **Boolean predicate** — `match` blocks accept literal-equality
  checks only. Keys default to top-level (`{ type: assistant }` matches
  when `frame.type == "assistant"`); dotted keys walk nested objects
  (`{ params.item.type: agentMessage }` was added in v1.0.343 for the
  codex profile, where the discriminator sits inside the JSON-RPC
  `params` envelope — see `reference/frame-profiles.md` Example 4).

That's the entire grammar. Everything claude-code's translator does
today fits within it. If a future rule needs richer expressions
(regex, arithmetic, `$map`-style transforms), we revisit the
language choice as a separate ADR — the subset is a JSONata-syntax
subset, so the migration path is intact.

We deliberately do *not* pull in `github.com/blues/jsonata-go` for
v1: the runtime cost (~3K LoC, separate CVE surface, edge-case
inheritance, additional learning curve for operators) outweighs the
expressiveness gain when 100% of today's translation logic fits a
~300 LoC hand-rolled evaluator.

**D3. Profiles carry an explicit `profile_version` integer.**

Profiles will themselves evolve. Loaders reject profiles whose
declared `profile_version` exceeds the highest supported version;
they accept lower versions only when forward-compatible (additive
changes). Without an explicit version, breaking changes silently
misbehave on old overlays.

**D4. Cutover via parallel-run, not a hard switch.**

Both translators (legacy Go `translate()` and profile-driven) run on
every frame for one or two release windows. Modes:

- **Test mode** — both run, only legacy output writes to DB,
  divergences logged to a structured channel (`frame_translator_diff`
  metric + audit row).
- **Canary mode** — both run, only profile output writes to DB,
  divergences logged. Operator-flippable per host.
- **Profile-only** — eventual default once divergence count holds at
  zero across a release.

A `frame_translator: legacy|profile|both` toggle in
`agent_families.yaml` controls behavior per family. Legacy stays
in-tree as an emergency escape for one release after profile becomes
default; then deleted.

**D5. Unmatched frames keep today's behavior.**

When no profile rule matches, host-runner emits
`kind=raw, payload=verbatim` exactly as today. Profiles are not
required to declare a catch-all. This preserves forward-compatibility
with new SDK frame types we haven't profiled yet — the transcript
keeps the bytes; mobile renders raw; we can write a profile rule
later without losing the prior data.

**D6. No profile inheritance; engines are standalone.**

Many fields (usage, rate_limit) will be near-identical across
engines. We accept the duplication. Inheritance complicates the
loader, the override semantics, and the diagnostic story (which
parent ruleset matched?). Copy-paste is auditable; inheritance is a
future ADR if duplication becomes painful.

**D7. Profiles are hub-served; mobile downloads + edits.**

Mobile fetches profiles from the hub (extending the existing
`/v1/teams/{team}/agent_families` endpoints) and posts edits back via
the same overlay-CRUD path used today. Symmetric with how mobile
already edits launch contracts. Mobile does not embed its own copy
of profiles — the hub is the source of truth.

## Consequences

**Becomes possible:**
- An SDK shape change ships as a YAML edit on the operator's host;
  `Invalidate()` re-reads the overlay and the next agent spawned
  picks up the change. Zero hub rebuild, zero release.
- New engine support is a YAML file authored alongside the launch
  declaration — no new Go file, no new test.
- Operators can fix upstream breakage faster than we can cut
  releases.

**Becomes harder:**
- Authoring profiles is a new skill (subset DSL, schema, fallback
  syntax). Documentation is mandatory; reference goes in
  `reference/frame-profiles.md`.
- Diagnosing a "rule missed" failure requires inspecting both raw
  frames and rule output. Plan calls for a debug overlay that
  flags low-confidence renders (e.g. rate-limit pill stayed empty
  → "no profile rule matched recent rate_limit_event frames").

**Becomes forbidden:**
- Hardcoded vendor field paths in `driver_stdio.go` after legacy
  retires. The `translate()` function shrinks to "decode JSON +
  dispatch through profile evaluator + handle no-match raw."
- Mixing parse logic into `translate()` for "just one quick fix" —
  go through the profile.

## References

- Discussion: `../discussions/multi-engine-frame-parsing.md` (full
  audit + Option A/B/C/D analysis + migration sketch)
- Plan: `../plans/frame-profiles-migration.md` (phases, parity
  test corpus, retirement of legacy translator)
- Existing infrastructure: `hub/internal/agentfamilies/families.go`
  (overlay loader + `Invalidate()` we're extending)
- Current coupling we're retiring:
  `hub/internal/hostrunner/driver_stdio.go::translate()` and
  `translateRateLimit` / `normalizeTurnResult` helpers
- Recent SDK churn that motivated this: v1.0.326 (system-subtype
  branch) and v1.0.328 (nested `rate_limit_info`)
- Blueprint axiom: `../spine/blueprint.md` §5.3.2 (engine
  pluggability — "new agent families land as drop-in declarations")
