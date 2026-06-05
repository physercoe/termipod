---
name: The engine launch contract belongs to the family, not the persona
description: A tester hit an M2 claude-code worker that failed to start because its template's backend.cmd was missing the stream-json protocol flags (ml-worker / briefing). The director's read is that this is a class of error, not a one-off — the mode-selecting argv is engine-and-mode trivia that gets hand-copied into every persona template (11 byte-identical claude-code M2 cmd strings; 14 duplicated permission_modes maps; a third copy in the mobile new-template scaffold), and is owned inconsistently (gemini's M2 driver injects the flags in Go, claude/codex carry them in the template string). This doc reviews the current launch path to its root cause from first principles, frames it against config-as-code practice, and lays out three options — A: a declarative per-mode `launch` block on the engine family (the symmetric input-side partner to `frame_profile`); B: a `{{mode_args}}` macro the renderer expands from the family; C: an invariant meta-test that rejects a cmd inconsistent with its driving_mode × engine — with a recommendation. Companion to ADR-010 (frame profiles as data) and the driving-mode model in protocols.md §5.
---

# The engine launch contract belongs to the family, not the persona

> **Type:** discussion
> **Status:** Open (2026-06-05) — raised by the director after a tester
> reported an M2 claude-code worker that failed to start because its template's
> `backend.cmd` lacked the stream-json flags (`ml-worker.v1.yaml`,
> `briefing.v1.yaml`). The director's framing: "the M2 cmd option can be
> modularized to avoid this class of error … the config and yaml/templates are
> also like code and need a clear, well-tested spec/principle." This is a fair
> call — the fix already landed for the two stragglers, but the *shape* that let
> them drift is structural.
> **Audience:** contributors
> **Last verified vs code:** v1.0.801-alpha

**TL;DR.** The argv that puts an engine into a driving mode — `--print
--output-format stream-json --input-format stream-json --verbose` for
claude-code M2 — is a property of the **(engine family × mode)**, not of the
persona. Today it is hand-typed into every persona template's `backend.cmd`
(11 byte-identical copies for claude-code M2 alone) and owned **inconsistently**:
gemini's M2 driver injects its flags in Go, while claude and codex carry them in
the template string. So a persona author must know engine-protocol trivia, and a
single omission silently fails the spawn at launch — exactly what happened. The
engine family (`agent_families.yaml`) already owns the *output* side of the
contract (`frame_profile`) and the mode list (`supports`); it should own the
*input* side too. Three options below; the recommendation is a declarative
per-mode launch block on the family (Option A), with an invariant test (Option C)
as the immediate guard.

## What happened

`ml-worker.v1.yaml` and `briefing.v1.yaml` both declare `driving_mode: M2`
(structured stdio — [protocols.md §5](../spine/protocols.md)) but their cmd was:

```yaml
cmd: "claude --model {{model}} {{permission_flag}}"
```

M2 is the agent-native JSON-line protocol: the host-runner's `StdioDriver`
reads line-delimited `stream-json` on the child's stdout and writes
`stream-json` user frames to its stdin. `claude` only speaks that protocol when
launched with `--print --output-format stream-json --input-format stream-json
--verbose`. Without those flags `claude` runs interactive, the driver never
gets a parseable frame, and the spawn fails. Every *other* M2 claude-code
template already carried the full flag set — the two stragglers had simply
never been updated. The fix was a one-line copy. The interesting question is
why a copy was the fix at all.

## How a cmd becomes a launched process (the current contract)

The launcher runs the template's `cmd` string essentially verbatim:

- **claude-code / codex M2** — `launchM2` takes `command := spec.Backend.Cmd`
  (`hub/internal/hostrunner/launch_m2.go:186`), prefixes `cd <workdir> &&`, and
  runs it through `bash -c`. The mode flags are whatever the template typed.
  There is **no** mode-flag injection.
- **gemini-cli M2** — the launcher *trims the cmd down to the binary token*
  (`launch_m2.go:312-326`) and the `ExecResumeDriver` builds its own argv:
  `args := []string{"--output-format", "stream-json", "--skip-trust"}`
  (`hub/internal/hostrunner/driver_exec_resume.go:350`). Here the mode contract
  lives in **Go**, and the template's `cmd: "gemini --acp"` is an M1 artifact
  the M2 path deliberately ignores.

So the same conceptual thing — "the flags that select this mode for this
engine" — is encoded in two different places depending on the engine, and for
claude/codex it is re-encoded once per persona. The duplication, measured:

| Copy | Count | Where |
|---|---|---|
| claude-code M2 cmd (byte-identical) | **11** | `hub/templates/agents/*.yaml` |
| `permission_modes` map (skip/prompt flags) | **14** | `hub/templates/agents/*.yaml` |
| claude-code M2 cmd (again) | 1 | mobile new-template scaffold, `lib/screens/team/templates_screen.dart:960` |
| gemini M2 flags | 1 | Go (`driver_exec_resume.go:350`) |

The `permission_modes` map (`skip: "--dangerously-skip-permissions"`,
`prompt: "--permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt"`)
is the **same class** of leak: it is claude-CLI-specific argv duplicated into
every persona. So is the literal `mcp__{{mcp_namespace}}__permission_prompt`
tool name.

## First principles: which layer owns what

There are two stable concerns here, and they have different rates of change and
different authors:

- **The engine adapter** — how the hub *detects, launches, drives, and parses*
  a given CLI in a given mode. It changes when an upstream CLI changes. Its
  author is whoever integrates the engine. It is already a first-class, data-
  driven object: `agent_families.yaml` owns `bin`, `version_flag`, `supports`
  (which modes), `runtime_mode_switch` (how to change mode), `prompt_image/pdf`
  (input modalities), and `frame_profile` (**how to translate the engine's
  output into typed `agent_events`** — ADR-010,
  [010-frame-profiles-as-data.md](../decisions/010-frame-profiles-as-data.md)).
- **The persona** — *who the agent is*: model, permission posture, prompt,
  capabilities, role, workdir. It changes when the team designs a new worker.
  Its author is a director/steward, who should not need to know that claude's
  stream-json input flag is spelled `--input-format`.

The asymmetry is the bug. The family already owns the engine's **output**
contract (`frame_profile`: stream-json → events). It does **not** own the
engine's **input/launch** contract (the argv → mode). That half leaked into the
persona. `frame_profile` and a launch spec are mirror images — one parses what
comes out, one composes what goes in — and they belong in the same object.

Stated as a principle:

> **The argv that selects a driving mode is a function of `(family, mode)`, not
> of the persona. A persona template should declare *intent* (model, permission
> posture, prompt) and never restate the engine's protocol wiring.**

This is the same law the blueprint already applies to output translation and to
"behavior is data" — we just stopped one step short of applying it to launch.

## Why this is the config-as-code problem the director named

Treating templates as code makes the failure modes legible:

- **DRY / single source of truth.** 11 identical strings means 11 chances to
  drift; two had already drifted. The correct number of copies of an engine's
  launch contract is one.
- **Mechanism vs. policy.** The mode flags are mechanism (how the engine is
  driven); the persona is policy (what it should do). Mixing them forces a
  policy author to edit mechanism.
- **Parse, don't validate.** The launcher should *derive* the argv from typed
  family data, not re-accept a free-text `cmd` per template and hope it's
  well-formed. A free-text command string is the least checkable representation
  of a structured fact.
- **Schema + tests.** `agent_families.yaml` already has a JSON schema
  (`agent_families.schema.json`) and a translator parity test
  (`profile_translate_parity_test.go`). The launch contract has *neither* a
  schema nor a test — so a malformed cmd is caught only by a human running the
  agent. The existing per-template `promptAlwaysParented` audit
  (`template_audit.go`) shows the team already encodes template invariants as
  Go tests; this class deserves the same.

## Options

### A. A declarative `launch` block on the engine family (recommended)

Give each family a per-mode launch spec next to `frame_profile`:

```yaml
# agent_families.yaml, family: claude-code
launch:
  M2:
    # flags that select structured-stdio mode for this engine; the launcher
    # composes argv = bin + mode_args + persona_args.
    mode_args: [--print, --output-format, stream-json, --input-format, stream-json, --verbose]
  M1: { mode_args: [] }   # claude-code SDK ACP, post-MVP
```

`launchM2` composes `bin (family.bin) + family.launch[mode].mode_args + persona
args (model, permission_flag)` instead of splitting a persona string. The
persona template stops carrying mode flags entirely — it declares `model`,
`permission_modes`, `prompt`. Gemini's Go-injected flags
(`driver_exec_resume.go:350`) fold into the same declarative place, ending the
two-places inconsistency.

- **Pro:** one source of truth; symmetric with `frame_profile`; schema- and
  test-able; a new persona can't omit what it never types; unifies the
  gemini/claude/codex split.
- **Con:** the largest change — `backend.cmd` is load-bearing across M1/M2/M4
  and many launcher tests; needs a migration that keeps `cmd` working (or
  derives it) during the transition. Also wants `permission_modes` hoisted to
  the family in the same move (it's the same class), which widens the blast
  radius.

### B. A `{{mode_args}}` macro the renderer expands from the family

Keep the `cmd` string but replace the literal flags with a macro:

```yaml
cmd: "claude --model {{model}} {{mode_args}} {{permission_flag}}"
```

The spec renderer expands `{{mode_args}}` from `family.launch[driving_mode]`
(same family field as Option A). Personas can't forget the flags because they
no longer type them, and the flags live once in the family — but the cmd-string
launch model, and every test that asserts on it, is preserved.

- **Pro:** root-cause fix with minimal blast radius; incremental; can ship
  before the full Option-A refactor and is forward-compatible with it.
- **Con:** still a string template (mechanism and policy share a line); doesn't
  by itself fix `permission_modes`; `{{mode_args}}` is one more macro to learn.

### C. An invariant meta-test (the immediate guard, pairs with A or B)

Independently of A/B, add a Go test that walks every bundled agent template and
asserts its `cmd` is consistent with its `driving_mode` × engine — e.g. an M2
claude-code template's cmd MUST contain `--output-format stream-json` and
`--input-format stream-json`; an M2 codex template MUST contain `app-server`.
This is the cheap, locally-testable net that would have failed CI on the
ml-worker/briefing drift instead of letting a tester find it.

- **Pro:** catches the whole class today, in minutes, with no design change;
  matches "invariants are executable tests" (CLAUDE.md).
- **Con:** a guard, not a cure — authors still hand-type the flags; it asserts
  the duplication is *correct* rather than removing it.

## Recommendation

Ship **C now** (the guard closes the hole immediately and is risk-free), and
adopt **A** as the target design — the launch contract is the input-side mirror
of `frame_profile` and belongs on the family, which makes the persona templates
shorter and un-droppable. Treat **B** as the migration on-ramp to A if a single
big change feels too broad: introduce `family.launch[mode]` + `{{mode_args}}`,
move the 11 cmd strings onto the macro, then in a follow-up collapse `cmd` into
structured fields and hoist `permission_modes` the same way. Whichever path,
fold `permission_modes` and the `mcp__…__permission_prompt` literal into the
same hoist — they are the same leak.

## Open questions

- **Migration shape.** Does `backend.cmd` stay (Option B macro, lowest risk) or
  get derived from structured fields (Option A end-state)? The launcher, the
  mobile editor, and `templates.list`/`get` all read `cmd` today.
- **Where the family launch spec composes.** `launchM2` for claude/codex vs. the
  gemini driver's self-built argv — Option A wants one composition point; the
  gemini exec-per-turn shape may still need a driver hook.
- **Scope of the hoist.** Just `mode_args`, or also `permission_modes` and the
  mcp namespace? Doing all three at once is cleaner but larger.
- **Back-compat for user-authored templates.** Existing on-disk personas (and
  the mobile scaffold) carry the literal flags; a macro/structured model must
  keep a verbatim `cmd` working so a hand-written template still launches.

## References

- Code: `hub/internal/hostrunner/launch_m2.go:186` (cmd run verbatim),
  `:312-326` (gemini bin-trim), `hub/internal/hostrunner/driver_exec_resume.go:350`
  (gemini Go-injected mode flags); `hub/internal/agentfamilies/agent_families.yaml`
  (`supports` / `frame_profile` / `runtime_mode_switch`);
  `hub/templates/agents/*.yaml` (the 11 cmd copies + 14 `permission_modes`);
  `hub/internal/server/template_audit.go` (an existing per-template invariant);
  `lib/screens/team/templates_screen.dart:960` (the scaffold's copy).
- Decisions: [ADR-010 — frame profiles as data](../decisions/010-frame-profiles-as-data.md)
  (the output-side precedent this generalizes),
  [ADR-021 — ACP capability surface](../decisions/021-acp-capability-surface.md).
- Reference: [driving modes (protocols.md §5)](../spine/protocols.md),
  [frame-profiles authoring](../reference/frame-profiles.md),
  [blueprint](../spine/blueprint.md) (behavior-is-data law).
