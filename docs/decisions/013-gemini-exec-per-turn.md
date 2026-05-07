# 013. Gemini integration is exec-per-turn-with-resume

> **Type:** decision
> **Status:** Superseded by `gemini-cli --acp` daemon path (2026-05-07) — exec-per-turn-with-resume retained as M2 fallback only
> **Audience:** contributors
> **Last verified vs code:** v1.0.347

**Amendment (2026-05-07).** Verification against `@google/gemini-cli@0.41.2`
showed `gemini --acp` is stable and exposes
`session/request_permission` for per-tool-call approval. The premise
in §Context that "Gemini-cli has no `app-server` equivalent and no
in-stream per-tool-call approval gate" was wrong at the time of
writing for the experimental flag, and is now wrong outright: ACP
graduated from `--experimental-acp` to `--acp`. The preferred shape
is now M1 (`launch_m1.go` → `ACPDriver`, the same driver the M1
blueprint slot was always reserved for). The exec-per-turn driver
described below remains as the M2 fallback for hosts whose gemini
build pre-dates ACP daemon mode; it is no longer the primary path.
See the gemini steward template (`steward.gemini.v1.yaml`) for the
current `driving_mode: M1` / `fallback_modes: [M2, M4]` declaration.

**TL;DR (original).** Gemini-cli has no `app-server` equivalent and
no in-stream per-tool-call approval gate, but headless mode now emits
a stable `session_id` and accepts `--resume <UUID>` for cross-process
session continuity (PR #14504, merged 2025-12-04). The driver is
therefore exec-per-turn — one subprocess per user turn — with the
session UUID captured from the first turn's `init` event and threaded
into subsequent spawns via `--resume`. The frame-profile substrate
(ADR-010) ports cleanly because gemini's stream-json events (`init`,
`message`, `tool_use`, `tool_result`, `result`, `error`) are
line-delimited JSON keyed on `type`, the same dispatch shape
claude-code already uses.

## Context

ADR-012 D6 named gemini-cli as the third engine to land after Codex
and explicitly chose exec-per-turn-with-resume over an "exec-from-
empty" framing that an earlier draft had assumed. This ADR pins the
implementation contract so the driver, frame profile, and steward
template can be authored without re-litigating the integration shape.

The relevant gemini-cli surface as of 2026-04-29:

- **`gemini -p <prompt> --output-format stream-json`** — headless,
  one-shot. Emits JSONL on stdout with event types `init`, `message`,
  `tool_use`, `tool_result`, `result`, `error`. The `init` event
  carries `session_id` (uuid v4 string) and `model` (e.g.
  `gemini-2.5-pro`). Spec: PR #10883 (stream-json output format,
  Oct 2025) + PR #14504 (`session_id` exposed in JSON output paths,
  fixes issue #14435, Dec 2025).
- **`gemini --resume <UUID>`** — re-attaches to a session stored on
  disk under the project-scoped session directory. Also accepts
  `latest` and a numeric session index; we use the UUID form
  exclusively because it's stable across listing changes.
- **`gemini --list-sessions`** — operator escape hatch; not used by
  the driver.
- **Approval gating** — only flag-time. `--yolo` (auto-approve all
  tool calls) and `--approval-mode auto_edit|yolo`. There is **no
  in-stream `requestApproval` event** the way Codex's app-server
  exposes; the engine either runs a tool or refuses, no deferred
  third-party gate.
- **MCP** — gemini reads `~/.gemini/settings.json` (or per-project
  `.gemini/settings.json`) with an `mcpServers` JSON object. Shape
  differs from Claude's `.mcp.json` and Codex's `.codex/config.toml`.

What this means for our integration:

| Concern | Claude (M2 stream-json) | Codex (app-server JSON-RPC) | Gemini (exec-per-turn-resume) |
|---|---|---|---|
| Process model | persistent stdio | persistent JSON-RPC daemon | spawn-per-turn |
| Multi-turn coherence | hub re-pipes user text | `turn/start` on live thread | `--resume <UUID>` argv |
| Per-tool-call approval | `permission_prompt` (sync via canUseTool) | `item/*/requestApproval` (deferrable JSON-RPC) | none (flag-time only) |
| Session resume across restart | hub-driven | `thread/resume` JSON-RPC | `--resume <UUID>` argv |
| Frame format | line-delimited JSON, `type` discriminator | line-delimited JSON-RPC, `method` + `params.item.type` | line-delimited JSON, `type` discriminator |
| MCP config | `.mcp.json` (JSON, project-local) | `~/.codex/config.toml` (TOML, user-level) | `~/.gemini/settings.json` (JSON, user-level) |
| Frame profile applies | yes | yes (with dotted-path matchesAll, ADR-012 D4) | yes (no extension needed) |

The frame profile substrate carries 2 of 3 engines without code
changes; gemini lands in the same lane as claude (`type`-keyed
JSONL) and reuses the existing matcher unchanged.

## Decision

**D1. Driver shape: spawn-per-turn, capture-and-resume.**

A new `driver_exec_resume.go` lives alongside `driver_stdio.go`
(claude) and `driver_appserver.go` (codex). One agent owns one driver
instance for its lifetime; the driver owns zero subprocesses at rest.
On `Input(text)`:

1. Build argv `gemini -p <text> --output-format stream-json [--yolo]
   [--resume <UUID>]`. The `--resume` flag is added iff the driver
   has previously captured a `session_id`.
2. Spawn the subprocess. Read JSONL from stdout; pipe each frame
   through `ApplyProfile` exactly as the existing `StdioDriver` does.
3. On the first `init` event, latch `session_id` into the driver's
   in-memory `sessionUUID` field and into `agent_events.session_id`
   on the `session.init` event we synthesize. Subsequent turns of
   the same agent reuse this UUID.
4. On `result` or process exit, mark the turn complete. The
   subprocess exits naturally; we do not kill it.

Driver state between turns: the captured `sessionUUID`, nothing else.
This is by design — gemini owns the session; we own the cursor.

**D2. Session UUID is hub-persisted at `agents.thread_id_json`.**

The first turn's captured `session_id` is written through to the
existing `agents.thread_id_json` column (a tiny JSON object that
Codex already uses — `{"backend":"codex","thread_id":"…"}`). For
gemini we write `{"backend":"gemini","session_id":"…"}`. On hub
restart and agent rehydration, the driver reads this column and
threads `--resume <UUID>` into the next spawn — no in-memory state
required for continuity. Resume across hub restarts works the same
way Codex's `thread/resume` works, just with argv as the transport.

**D3. Frame profile is a new entry in `agent_families.yaml`.**

`family: gemini-cli` already exists with `supports: [M1, M4]`. This
ADR adds `M2` to that list and a `frame_profile` block. Rules cover
the six event types:

- `init` → `session.init` (payload: session_id, model)
- `message` (`role=assistant`, `delta=false`) → `text`
- `message` (`role=assistant`, `delta=true`) → not emitted in v1
  (parity with codex; we emit on completion)
- `tool_use` → `tool_call` (tool_name, tool_id, parameters)
- `tool_result` → `tool_result` (tool_id, status, output)
- `error` → `raw` (severity from `status`/`message`)
- `result` → `turn_complete` (stats payload)

No new evaluator features required. The matcher already supports
`{ type: <literal> }` and `{ type: <literal>, role: <literal> }`
top-level keys; gemini's discriminator is at the top level.

**D4. No `permission_prompt` for gemini.**

ADR-005 named `permission_prompt` per-engine: sync via canUseTool
on Claude, deferrable JSON-RPC on Codex. Gemini joins the
"unsupported" column. The implications:

- Stewards running on gemini that need principal-level decisions
  use the existing `request_approval` MCP tool (turn-based
  attention, ADR-011 D1) rather than per-tool-call gating.
- The gemini steward template defaults to `--yolo` for tool
  execution; the steward is responsible for routing risky decisions
  through `request_approval` itself.
- This is a **vendor gap**, not a hub bug — surfaced in operator
  docs and the family entry as `incompatibilities: [permission_prompt]`.

If gemini-cli ever ships an in-stream approval event, we add the
bridge in a follow-up ADR. For now: no permission_prompt, period.

**D5. MCP config materialization adds a third format.**

`writeMCPConfigForFamily` (introduced in ADR-012 D5 / slice 5) gains
a `gemini` branch that writes `<workdir>/.gemini/settings.json`. The
shape mirrors claude's `.mcp.json` — gemini-cli's `mcpServers`
schema accepts the stdio `command + env` transport identically, and
keeping the wire shape parallel keeps `hub-mcp-bridge` itself
unaware of which engine is on the other side:

```json
{
  "mcpServers": {
    "termipod": {
      "command": "hub-mcp-bridge",
      "env": {
        "HUB_URL": "<url>",
        "HUB_TOKEN": "<token>"
      }
    }
  }
}
```

File mode `0o600` and `mkdir -p .gemini` follow the codex precedent.
Per-project (`<workdir>/.gemini/`) rather than user-level
(`~/.gemini/`) because we want the same per-spawn isolation we get
for codex — multiple agents on the same host writing into the same
global config would race. gemini-cli reads project-scoped
`<workdir>/.gemini/settings.json` automatically when the cwd matches
(no equivalent of codex's trusted-projects gate to bypass).

Server alias `termipod` (the existing `hub.MCPServerName` constant)
already avoids underscores, which gemini-cli's docs warn about for
policy-engine parsing — no rename needed.

**D6. Cancellation is SIGTERM the subprocess; resume preserves
continuity.**

When the user cancels a turn, the driver sends SIGTERM to the
running gemini subprocess and emits a synthetic `turn_complete` with
`status=canceled`. The captured `session_id` is unchanged, so the
next user turn re-spawns with `--resume <UUID>` and the conversation
continues from the cancellation point. Gemini's session storage is
checkpoint-based — partial turns are recorded enough that resume
works even if the cancelled turn never reached `result`. Verified by
walkthroughs in the dev blog (cited in §References).

If SIGTERM doesn't take effect within 5s the driver escalates to
SIGKILL. This matches the existing claude driver.

**D7. The `Driver` interface stays as it is (ADR-012 slice 3).**

`Driver` already has `Start() / Input(text) / Close()` plus an
optional `AttentionPoster`. Gemini's driver implements:

- `Start()` — no-op. (The first spawn happens on first `Input`.)
- `Input(text)` — spawns subprocess, streams stdout through
  `ApplyProfile`, posts events, waits for exit.
- `Close()` — sends SIGTERM to any running child, waits up to 5s,
  then SIGKILL.

No changes to the interface; `launch_m2.go` gets a third dispatch
arm: `family=gemini-cli` → `*ExecResumeDriver`.

## Consequences

**Becomes possible:**

- A third engine ships with the same data-driven substrate as the
  first two — no Go diff for new event shapes, just YAML rules.
- The "vendor parity" claim in `roadmap.md` and
  `feedback_no_short_board.md` becomes verifiable: a steward can
  swap engines with a template change, modulo the
  `permission_prompt` gap (D4).
- Gemini-specific stewards (e.g. ones that prefer Gemini's longer
  context window or Google-side tool integrations) become
  authorable as built-in templates.

**Becomes harder:**

- Three driver shapes to maintain. Stays manageable because the
  frame-profile substrate carries the per-vendor parsing — drivers
  themselves are <300 LoC each and focus on framing/lifecycle.
- Per-turn process startup (~hundreds of ms) is felt at human
  cadence on weak hosts. Acceptable for human conversation; not
  acceptable for fanout-style workers, which should prefer the
  daemon engines (Claude / Codex). Surfaced in family
  `recommended_for` metadata in a follow-up wedge.
- Approval-policy authoring asymmetry: `--yolo` is a blunt
  instrument. Stewards on gemini must self-route risky tool calls
  through `request_approval`. Not enforceable at the hub layer for
  v1; revisited only if it produces incidents.

**Becomes forbidden:**

- Spawning gemini without `--output-format stream-json`. Pure
  text output is unsupported by the driver and would fall back to
  `kind=raw` for everything.
- Spawning gemini without `--resume` after the first turn of an
  agent's lifetime. Multi-turn agents that re-spawn fresh would
  lose conversational context — the test suite asserts
  `--resume <UUID>` is present whenever
  `agents.thread_id_json.session_id` is populated.

## References

- ADR-010 frame profiles as data — substrate this builds on.
- ADR-011 turn-based attention delivery — D6 names
  `permission_prompt` as a vendor gap that gemini joins.
- ADR-012 codex app-server integration — D6 named gemini's shape;
  this ADR pins it.
- gemini-cli stream-json format: PR #10883 (merged Oct 2025);
  field schema confirmed at
  `https://geminicli.com/docs/cli/headless/`.
- gemini-cli session_id surfacing: PR #14504 (merged 2025-12-04),
  fixes issue #14435.
- gemini-cli resume mechanics: Google Developers Blog, "Pick up
  exactly where you left off with Session Management in Gemini
  CLI" (2026-Q1).
- Implementation lands as slices 2-6 (frame profile, driver, MCP
  config, approval-gap doc, steward template); slice 7 — live
  cross-vendor `request_help` smoke against a real gemini binary —
  remains unfunded and gated on a test host with gemini installed
  (same gate as ADR-012 slice 7).
