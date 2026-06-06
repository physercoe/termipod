# Engine launch contract on the family — rollout

> **Type:** plan
> **Status:** Done (2026-06-05) — P0–P3 shipped. The phased
> implementation of [ADR-043](../decisions/043-engine-launch-contract-on-the-family.md)
> (the per-mode launch contract lives on the engine family as data, not
> in each template's `backend.cmd`); the ADR holds the *what/why*, this
> plan the *how* (and now the shipped record).
> **Audience:** contributors
> **Last verified vs code:** v1.0.807-alpha

**TL;DR.** A tester hit an M2 claude-code worker that failed to start
because its template's `backend.cmd` lacked the stream-json flags.
Generalize ADR-010 (frame profiles as data) to the input/launch side:
the launch args for a driving mode become **family data**, composed at
spawn, with a guard test. Built hub-only and Go-testable, all shipped.

## Phases

- **P0 — guard (Option C), DONE.** `TestBundledAgentTemplates_M2LaunchContract`
  pins the current cmd-string contract (claude-code M2 ⊇ stream-json flags;
  codex M2 ⊇ `app-server`). Closes the regression hole immediately.
- **P1 — family data, DONE.** `launch.<mode>.mode_args` in the schema + the
  `agentfamilies` Go struct (`LaunchMode`) + `Family.LaunchArgs(mode)`;
  populated claude-code (M2), codex (M2), gemini-cli (M2) from the values that
  existed in the templates / gemini driver. Parse-only; `families_test.go`
  pins the bundled values, the nil-for-absent-mode contract, and copy-on-return.
- **P2 — compose + drop flags, DONE.** `launchM2` composes
  `Family.ComposeLaunchCmd("M2", cmd)` (claude-code, codex); the gemini
  `ExecResumeDriver` reads `LaunchArgs("M2")` via a new `BaseArgs` field; the
  11 claude templates + codex template + the mobile scaffold dropped their mode
  flags. Composition is append-only with documented precedence — a no-op when
  the cmd already carries the contract — so the ripple was a single test, and
  user-authored full cmds keep working. The P0 guard flipped to asserting the
  composed command **and** that the raw template no longer carries the flags
  (locking the single source). `launchM1` mode args are not yet declared (M1
  ACP carries no stream-json cmd today); add them when an M1 contract needs it.
- **P3 — hoist `permission_modes`, DONE.** Added `permission_modes` to the
  claude-code family (`{skip, prompt}`) + `Family.PermissionFlag(mode)`; the
  spawn resolver (`buildSpawnVars`) falls back to it when the persona spec
  yields no `{{permission_flag}}`, sourcing the engine from the merged spec's
  `backend.kind` (not `in.Kind`, which is the agent/template id). Dropped the
  map from the 11 M2 claude templates + the hub-side scaffold; `steward.claude-m4`
  keeps its M4-specific `skip` as the deliberate override (explicit wins). A
  guard asserts every flag-dropped claude template is covered by the family.
  The `mcp__…__permission_prompt` literal rides inside the hoisted `prompt`
  value (with `{{mcp_namespace}}`), so it moved with it.

## References

- [ADR-043](../decisions/043-engine-launch-contract-on-the-family.md) —
  the locked decision this plan implements.
- [ADR-010](../decisions/010-frame-profiles-as-data.md) — the
  output-side "profiles as data" precedent this generalizes.
- [`discussions/engine-launch-contract.md`](../discussions/engine-launch-contract.md)
  — the option analysis behind the decision.
