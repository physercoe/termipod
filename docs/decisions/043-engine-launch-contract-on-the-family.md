# 043. The engine launch contract lives on the family, not the persona

> **Type:** decision
> **Status:** Accepted (2026-06-05) — the director chose "guard test now +
> family `launch` block (Option A)" from
> [`discussions/engine-launch-contract.md`](../discussions/engine-launch-contract.md),
> after a tester hit an M2 claude-code worker that failed to start because its
> template's `backend.cmd` lacked the stream-json flags (`ml-worker.v1`,
> `briefing.v1`). Generalizes the output-side precedent of
> [ADR-010](010-frame-profiles-as-data.md) (frame profiles as data) to the
> input/launch side, over the driving-mode model in
> [protocols.md §5](../spine/protocols.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.801-alpha

**TL;DR.** The argv that selects a driving mode for an engine — `--print
--output-format stream-json --input-format stream-json --verbose` for
claude-code M2 — is a property of the **(engine family × mode)**, not of the
persona. Today it is hand-copied into every persona template's `backend.cmd`
(11 byte-identical claude-code M2 strings) and owned inconsistently (gemini's
M2 driver injects its flags in Go; claude/codex carry them in the template), so
a single omission silently fails a spawn. We move the mode-selecting argv onto
the engine family (`agent_families.yaml`) as a declarative per-mode `launch`
block — the input-side mirror of the family's `frame_profile` — and the
launcher composes it. Persona templates stop carrying mode flags. A meta-test
guards the invariant in the meantime.

## Context

`agent_families.yaml` is already the data-driven engine adapter: it owns `bin`,
`supports` (which modes), `runtime_mode_switch`, the input-modality flags, and
`frame_profile` — the declarative translation of the engine's **output** into
typed `agent_events` (ADR-010). It does **not** own the engine's **input/launch
contract** (the argv that puts the engine into a mode). That leaked into the
persona templates, where:

- `launchM2` runs `spec.Backend.Cmd` verbatim for claude-code/codex
  (`hub/internal/hostrunner/launch_m2.go:186`), so the mode flags must be in the
  template — there is no injection.
- gemini-cli M2 is different: the launcher trims the cmd to the bin
  (`launch_m2.go:312-326`) and the `ExecResumeDriver` builds its own argv
  (`--output-format stream-json --skip-trust`,
  `hub/internal/hostrunner/driver_exec_resume.go:350`).

So the same fact lives in two places, and for claude/codex it is re-typed once
per persona: 11 byte-identical claude-code M2 cmd strings, 14 duplicated
`permission_modes` maps, plus a third copy of the cmd in the mobile new-template
scaffold (`lib/screens/team/templates_screen.dart`). `ml-worker.v1` and
`briefing.v1` had simply never been updated to the stream-json cmd, so they
shipped a launch contract that doesn't launch. The full analysis, options, and
config-as-code framing are in the discussion doc.

## Decision

1. **The mode-selecting argv is `(family, mode)` data.** Add a declarative
   per-mode `launch` block to each family in `agent_families.yaml`, e.g.

   ```yaml
   family: claude-code
   launch:
     M2:
       mode_args: [--print, --output-format, stream-json,
                   --input-format, stream-json, --verbose]
   ```

   Schema (`agent_families.schema.json`) and the Go struct gain the field; a
   `Family.LaunchArgs(mode)` accessor exposes it.

2. **The launcher composes; the persona declares intent.** The launcher adds
   `family.LaunchArgs(driving_mode)` to the rendered persona command. Persona
   templates carry only the engine bin + persona intent (`{{model}}`,
   `{{permission_flag}}`) and drop the mode flags entirely. Composition is
   additive (append to the rendered cmd) so env prefixes (codex `CODEX_HOME=…`),
   `--resume` splicing, and the `bash -c "cd … && …"` wrapper are unaffected;
   for flag-based CLIs argv order is immaterial. gemini-cli's Go-injected flags
   move to read `LaunchArgs(M2)` from the family, ending the two-places split.

3. **The contract is a test, not a convention.** A meta-test asserts every
   bundled template's launch command satisfies its `(driving_mode × engine)`
   contract — first against the cmd string (Option C, shipped now:
   `hub/internal/server/agent_template_launch_test.go`), then against the
   composed command once §2 lands, so it stays green after the templates drop
   the literal flags.

4. **`permission_modes` and the mcp-namespace literal follow.** They are the
   same leak (claude-CLI argv duplicated per persona) and SHOULD hoist onto the
   family in the same arc, but are sequenced after the mode-args move to keep
   each change reviewable.

## Implementation

The phased rollout (P0 guard test, P1 family data, P2 compose + drop the
template flags, P3 hoist `permission_modes` — all shipped, with
file-level detail) lives in the plan,
[`plans/engine-launch-contract-rollout.md`](../plans/engine-launch-contract-rollout.md).
This ADR records the decision; the plan owns the *how* and the status.

## Consequences

**Easier:**
- One source of truth for each engine's launch contract; a new persona can't
  omit flags it never types. The class of "template runs interactive and the
  spawn fails" is gone.
- The family becomes the complete engine adapter — `supports` (modes) +
  `launch` (input contract) + `frame_profile` (output contract) + the modality
  flags — readable top to bottom.
- A new engine or a new mode for an existing engine is one YAML edit, matching
  the blueprint's behavior-is-data law.

**Harder / now constrained:**
- The launcher gains a composition step; the ≈13 launch tests that assert on the
  command string move to asserting the composed result (absorbed in P2).
- A user-authored template that still hand-writes the full cmd must keep
  working: composition must be append-only and tolerant of a cmd that already
  contains the flags (idempotent or documented precedence), so back-compat holds
  during and after the migration.
- Two engines compose differently (claude/codex via the launcher; gemini via
  its driver). P2 routes both through `LaunchArgs` but the call sites differ.

**Out of scope:** M4 launch (LocalLogTail / pane) — those paths don't run a
stream-json cmd; the antigravity/kimi families stay as-is. Restructuring `cmd`
into fully structured fields (dropping the string entirely) is a later step the
`launch` block makes possible but does not require.

## References

- Discussion: [`engine-launch-contract.md`](../discussions/engine-launch-contract.md)
  (the review, first-principles, and the three options).
- Code: `hub/internal/agentfamilies/agent_families.yaml` (+ `.schema.json`,
  `families.go`); `hub/internal/hostrunner/launch_m2.go`,
  `driver_exec_resume.go`; `hub/templates/agents/*.yaml`;
  `hub/internal/server/agent_template_launch_test.go` (the P0 guard);
  `lib/screens/team/templates_screen.dart` (the scaffold copy).
- Related ADRs: [010](010-frame-profiles-as-data.md) (frame profiles as data —
  the output-side precedent), [021](021-acp-capability-surface.md).
- Reference: [driving modes (protocols.md §5)](../spine/protocols.md),
  [frame-profiles authoring](../reference/frame-profiles.md).
