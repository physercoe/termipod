# 054. kimi-code-ts: the TypeScript Kimi Code is a separate family, M1-only

> **Type:** decision
> **Status:** In review
> **Audience:** contributors
> **Last verified vs code:** v1.0.821 / kimi-code 0.27.0 on macOS arm64, 2026-07-19

**TL;DR.** The TypeScript rewrite of Kimi Code CLI
(`MoonshotAI/kimi-code`, single compiled binary, data root
`~/.kimi-code`) joins the registry as a **new family `kimi-code-ts`**,
coexisting with the existing `kimi-code` family (legacy Python
`kimi-cli` line). It rides the same M1/ACPDriver path (`kimi --yolo
acp`, M4 fallback), but MCP injection moves from the removed
`--mcp-config-file` flag to the auto-discovered project-level
`<workdir>/.kimi-code/mcp.json` — no argv splice, no user-level file
copies. The TS build's ACP `session/new` speaks the newer
`configOptions` shape instead of `availableModes`/`availableModels`,
so the mode/model picker does not hydrate for this family until a
follow-up ACPDriver translation wedge lands. Verified on-host against
kimi-code 0.27.0; this ADR lifts the HOLD declared in the
[kimi-code-engine plan §9.1](../plans/kimi-code-engine.md).

## Context

ADR-026 integrated the Python `kimi-cli` (floor 1.43.0) as the fourth
engine. Upstream then forked its own product: the Python line keeps
being patched (1.47.0, 2026-06), while a next-gen TypeScript rewrite
ships under the same binary name `kimi` with a different config
surface. The §9.1 watch item held off adapting until we could run the
real binary and answer three load-bearing questions: does `kimi acp`
survive, does `--mcp-config-file` survive, and what replaces
`--thinking`.

On-host verification against **kimi-code 0.27.0** (2026-07-19):

- `kimi acp` survives; `initialize` returns `protocolVersion: 1`,
  `loadSession: true`, `promptCapabilities: {image: true, audio:
  false}`, `mcpCapabilities: {http, sse}`, `sessionCapabilities:
  {list, resume}`.
- `--mcp-config-file` is **removed**. MCP config is auto-discovered
  from `mcp.json` at two levels — user (`$KIMI_CODE_HOME/mcp.json` or
  `~/.kimi-code/mcp.json`) and project (`<cwd>/.kimi-code/mcp.json`,
  taking precedence) — with a `mcpServers` wrapper and stdio entries
  shaped `{command, args, env, cwd}`.
- `--thinking` is **removed**; thinking is config-driven
  (`~/.kimi-code/config.toml`) and per-session via ACP config options.
  Surviving top-level flags include `--yolo`, `--auto`, `--plan`,
  `-m`, `-p`, `--output-format text|stream-json`, `-S`, `-c`.
- Auth advertises `{id: "login", type: "terminal"}` (device code);
  ACPDriver does not drive terminal-auth, so login stays out-of-band
  (`kimi login`), unchanged from ADR-026's posture.
- `session/new` returns `configOptions` selects (`model`, `thinking`,
  `mode` = default/plan/auto/yolo) rather than the Python line's
  `modes`/`availableModels` blocks. `session/set_model` exists on the
  wire (`-32602` on unknown session, not `-32601`).
- A headless one-shot mode now exists (`kimi -p ... --output-format
  stream-json`, NDJSON `role`/`content` rows) — an M2 candidate for a
  future wedge, not this one.

## Decision

- **D1. New family, not a re-point.** `kimi-code-ts` is added
  alongside `kimi-code`; the Python family is untouched because the
  Python line is still supported upstream and teams may depend on it.
  The `-ts` suffix is explicit and matches the §9.1 watch language;
  `kimi-code-next` was rejected (ages poorly). When upstream EOLs the
  Python line, a future ADR can retire `kimi-code` and, if desired,
  re-alias.
- **D2. M1-only, M4 fallback, `kimi --yolo acp`.** Same driving shape
  as ADR-026: ACPDriver covers the wire with no core diff. `--yolo`
  keeps the kimi steward's consent posture (engine-layer auto-approve,
  self-gating via `request_approval`); `--thinking` is dropped because
  the flag no longer exists.
- **D3. MCP injection is file-only, project-level.** The hub writes
  `<workdir>/.kimi-code/mcp.json` (deep-merge with any existing
  project file, fail-loud on malformed, replace-not-skip on the
  `termipod` entry, 0o600/0o700). No argv splice — the spawn cmd
  already `cd`s into the workdir, and the TS build's project-over-user
  precedence makes the per-spawn entry win while the operator's
  user-level servers keep loading untouched. This is strictly cleaner
  than ADR-026 D5: no flag ordering constraints, no copying of
  operator config into workdirs.
- **D4. Capability flags pinned to verified values.**
  `prompt_image.M1: true` (advertised), `prompt_pdf.M1: false` (not
  advertised — ADR-026's assumed-true is not copied),
  `default_auth_method: ""`, `runtime_mode_switch.M1: rpc`
  (`session/set_model` exists; inert until D6 lands).
- **D5. Resume joins the ACP splice.** `loadSession: true` →
  `kimi-code-ts` is added to the `spliceACPResume` arms of
  `handleResumeSession` and `respawnWithSpecMutation`. No driver
  change.
- **D6. `configOptions` translation is a follow-up, not a blocker.**
  ACPDriver parses only `availableModes`/`availableModels`; the TS
  build's `configOptions` reply therefore yields no mode/model state
  events and the picker stays hidden. Switching exists on the wire;
  only UI hydration is missing. A translation wedge (engine-neutral,
  would also cover future ACP adopters of the same revision) is
  tracked separately.

## Consequences

- Operators see a sixth engine in pickers that enumerate families
  (desktop updated here; mobile renders unknown kinds generically
  until its label case lands).
- **Bin-name collision:** both builds install as `kimi`, and the
  capability probe is bin-existence-based, so a host with either build
  reports both families available. `kimi --version` disambiguates by
  eye (Python prints `kimi, version 1.x.y`; TS prints bare `0.x.y`).
  The family row comments carry this caveat. Programmatic
  disambiguation (version-string shape sniffing in the probe) is
  possible but deferred — the failure mode is a spawn-time flag error,
  loud and recoverable, not silent corruption.
- The M2 stream-json surface is documented in the plan but unwired;
  adding it means a new frame profile (the NDJSON schema matches
  neither claude's nor gemini's).

## Alternatives considered

- **Re-point `kimi-code` at the TS build.** Smaller diff, but breaks
  every host still on the Python build (flag set and MCP mechanism
  both differ) with no migration path. Rejected.
- **Wait for the TS line to hit 1.0.** The §9.1 HOLD was conditional
  on not having the binary; we now have verified answers, the
  integration surface is minimal, and holding longer just delays
  operator value. Rejected.
- **ACP-level MCP passthrough** (`mcpServers` in `session/new`, which
  the TS build advertises). Would avoid writing files, but ACPDriver
  hardcodes an empty list and hub-mcp-bridge is stdio — the
  project-level file achieves the same isolation with zero driver
  churn. Rejected for this wedge.

## References

- [ADR-026](026-kimi-code-engine.md) — the Python-line integration.
- [Plan: kimi-code-ts engine](../plans/kimi-code-ts-engine.md) —
  implementation detail, surfaces, tests, known gaps.
- [ADR-021](021-acp-capability-surface.md) — capability grammar.
- Upstream: `MoonshotAI/kimi-code`; Kimi Code MCP docs
  (`kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html`).
