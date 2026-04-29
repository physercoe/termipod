# 012. Codex integration target is `codex app-server`, not `codex exec`

> **Type:** decision
> **Status:** Accepted (2026-04-29)
> **Audience:** contributors
> **Last verified vs code:** v1.0.340

**TL;DR.** When we add Codex as a second engine alongside Claude Code,
the integration point is `codex app-server` (the long-lived JSON-RPC
daemon over line-delimited stdio), not `codex exec --json` (the
one-shot subprocess). App-server is OpenAI's canonical headless
harness — it gives us deferrable in-stream approval requests, persistent
threads with first-class `thread/resume`, MCP hot-reload, and explicit
turn lifecycle notifications. Going through `exec` would force us to
re-implement all of that ourselves and would lose the per-tool-call
approval surface entirely. Gemini takes the exec-per-turn shape with
`--resume <UUID>` for cross-process session continuity (PR #14504,
Dec 2025) — gemini-cli has no app-server equivalent and no in-stream
approval gate, but it does have proper headless resume now.

## Context

Adding Codex was the obvious next move once ADR-010 made frame profiles
data-driven and ADR-011 made principal-level attention turn-based. The
research for that wedge surfaced two viable Codex integration shapes:

1. **`codex exec --json`** — Each user turn invokes a fresh subprocess,
   reads JSONL events from stdout, exits. Resume via
   `codex exec resume <thread-id>`. Approvals controlled by
   `--ask-for-approval` flags only; **no in-stream approval events** in
   `--json` mode — to run headless we'd `--full-auto` and rely entirely
   on hub-mediated MCP gating for principal-level decisions.

2. **`codex app-server`** — A long-lived JSON-RPC 2.0 daemon. Three
   transports: stdio (default, stable), Unix socket (WebSocket), TCP
   WebSocket (experimental, with `Authorization: Bearer` auth). One
   process hosts many threads. The protocol exposes thread lifecycle
   (`thread/start`, `thread/resume`, `thread/fork`), turn lifecycle
   (`turn/start`, `turn/started`, `turn/completed`, `turn/steer`,
   `turn/interrupt`), per-item streaming (`item/started`,
   `item/agentMessage/delta`, `item/commandExecution/outputDelta`,
   `item/completed`), and — crucially — server-initiated approval
   *requests* the client must respond to:

   - `item/commandExecution/requestApproval`
     (decisions: `accept | acceptForSession | acceptWithExecpolicyAmendment | decline | cancel`)
   - `item/fileChange/requestApproval`
     (decisions: `accept | acceptForSession | decline | cancel`)
   - `item/permissions/requestApproval`
     (scope: `session | turn`)
   - `mcpServer/elicitation/request`
   - `item/tool/requestUserInput`

   These are JSON-RPC requests with `id`, not one-way notifications;
   the client controls when to respond, and the protocol does not
   impose a wall-clock timeout. OpenAI positions app-server as the
   "unifying agent surface" for ChatGPT Desktop, IDE clients, and
   third-party harnesses (InfoQ, Feb 2026).

The trade-off matrix is asymmetric:

| Concern | `exec --json` | `app-server` |
|---|---|---|
| Process model | spawn-per-turn | persistent, multi-thread |
| Session resume | argv-shuffle (`exec resume <id>`) | `thread/resume` JSON-RPC method |
| Turn lifecycle | inferred from process exit | explicit `turn/started` / `turn/completed` |
| Per-tool-call approval | not exposed in JSONL stream | first-class deferrable RPC requests |
| Multi-turn input | re-spawn each time | `turn/start` on live thread |
| MCP hot-reload | not possible (process must exit) | `config/mcpServer/reload` |
| Backpressure / health | up to us | built-in (error -32001, `/readyz`, `/healthz`) |
| Per-turn process overhead | hundreds of ms | one cold start per agent |

The deferrable approval-request shape is what tipped the decision.
ADR-011 separated the four attention kinds: `request_approval`,
`request_select`, `request_help` are turn-based (vendor-neutral);
`permission_prompt` was sync-only because Claude's `canUseTool` hook
contract has no deferred branch (vendor-contract limitation, ADR-011
D6). Codex's app-server gives us the *equivalent* of `permission_prompt`
— per-tool-call gating, in-stream — but with no sync limitation,
because both sides are on a long-lived stdio pipe and the protocol
permits arbitrary response latency. That's structurally what ADR-011
wanted but couldn't have on Claude. Going through `exec` would throw
this away: we'd run `--full-auto` and the principal would lose the
per-command gate that Claude users have today.

Counter-argument considered: app-server is more code (a JSON-RPC
client + thread manager + approval bridge) than `exec`, and the WS
transport is marked "experimental and unsupported." The mitigation is
that **stdio transport is stable** (it's the default; the IDE clients
all use it) and the JSON-RPC protocol surface itself is stable
enough that OpenAI generates TypeScript and JSON Schema bindings
from the running binary (`codex app-server generate-ts`,
`generate-json-schema`). For our hub-spawns-codex-locally model,
stdio is exactly what we want — same shape as our existing M2
stream-json path, just JSON-RPC framing on top.

For Gemini, the same research found no app-server equivalent —
gemini-cli is a one-process-per-turn shape — but `--resume <UUID>`
is supported in headless mode and the `init` event's `session_id`
field that PR #14504 added (merged December 2025) is what we
capture as the resume cursor. So Gemini gets the exec-per-turn
driver but with proper conversational continuity across turns; the
two costs that don't go away are per-turn process startup and the
absence of an in-stream per-tool-call approval gate. Both
acceptable for vendor parity.

## Decision

**D1. App-server is the Codex integration point.**

The hub's host-runner spawns `codex app-server --listen stdio://` per
agent; the hub-side driver speaks line-delimited JSON-RPC 2.0 on the
process's stdio. We do **not** use `codex exec --json`. We do **not**
use the WebSocket transport for the MVP — stdio matches our
host-runner-spawns-locally model and avoids the experimental WS
surface.

**D2. Threads map to sessions; turns map to user-text turns.**

`thread/start` opens a new agent session row;
`thread/resume <thread-id>` re-attaches when the host-runner restarts.
`turn/start` is how a user-text frame reaches the model;
`turn/completed` ends the agent's visible turn. `turn/steer` is the
in-flight equivalent and is reserved for a follow-up wedge (we don't
need it for the MVP one-question-at-a-time cadence).

**D3. App-server approval requests bridge to attention items.**

When app-server emits `item/commandExecution/requestApproval` (or the
sibling file-change / permissions variants), the driver:

1. Persists the in-flight JSON-RPC request id alongside the new
   `attention_items` row (kind = `permission_prompt`).
2. Ends the agent's visible turn — the engine sits idle on the
   request, no tokens consumed.
3. On `/decide` resolution, the dispatcher looks up the parked
   request id and sends the JSON-RPC response on the same stdio pipe
   (`{ result: { decision: "accept" | "decline" | ... } }`). If the
   driver process restarted in between, the request is dropped on
   the codex side; the agent retries the failed turn on next user
   input — clean recovery, persistence lives in `attention_items`.

This is the **vendor-neutral equivalent of `permission_prompt`**
without the sync-block limitation that ADR-011 D6 documented for
Claude. Codex gets the right shape for free.

**D4. The frame-profile system extends to JSON-RPC envelopes.**

ADR-010's frame profiles are already line-delimited JSON translators
keyed on a discriminator field. JSON-RPC notifications are
line-delimited JSON with a `method` discriminator and a `params`
payload — strictly a subset of the profile system's existing model.
Codex's frame profile keys on `method` rather than Claude's `type`,
and writes `params.foo` paths instead of `$.foo`. The evaluator may
need a small extension if multi-key dispatch (`method` plus
`params.item.type`) doesn't compose cleanly with the existing
`for_each` machinery; that scope lands with the profile-author wedge,
not this ADR.

**D5. MCP config materialization is per-family.**

`writeMCPConfig` becomes a dispatch table keyed on `agent_families.yaml`
family name. Claude continues to write `.mcp.json` (JSON, project-
local). Codex writes `~/.codex/config.toml` (TOML, user-level — the
project-scoped variant requires the project to be in Codex's trusted-
projects list, more friction than it's worth for the MVP). Bearer
token rides in `bearer_token_env_var = "TERMIPOD_HUB_TOKEN"` with
the env var injected per-spawn. Gemini will write `~/.gemini/settings.json`
(JSON, different shape) when its slice lands.

**D6. Gemini stays on exec-per-turn (with resume).**

Gemini gets a separate `driver_exec.go` that spawns
`gemini -p - --output-format stream-json [--resume <uuid>]` per user
turn. Multi-turn coherence is preserved across spawns via
`--resume <UUID>`; the UUID is captured from the `init` event's
`session_id` field that PR #14504 added (merged December 2025). So
the conversational shape is the same as Codex's `exec resume <id>`
mode — process-per-turn, state on disk between turns — not
fresh-from-empty as an earlier draft of this ADR assumed.

What's still missing relative to Codex's app-server:

- One process per turn rather than one long-lived daemon. Per-turn
  startup overhead is hundreds of ms — fine at human cadence, called
  out in vendor-comparison docs.
- No per-tool-call approval gate. Gemini exposes only `--yolo` and
  `--approval-mode auto_edit|yolo`, both flag-time decisions; there
  is no in-stream "the agent wants to run X, allow?" event the way
  Codex's `item/commandExecution/requestApproval` works. Strategic-
  tier gating still works through ADR-011's `request_approval`
  attention (the agent calls our MCP tool itself), but mid-turn
  per-command denial is a Codex/Claude feature only.
- No app-server equivalent (no JSON-RPC daemon hosting many threads
  in one process). For us this affects efficiency at scale, not
  correctness.

We revisit if gemini-cli ships an app-server equivalent.

**D7. The post-MVP `permission_prompt` bridge wedge becomes Claude-only.**

ADR-011's deferred work — bridge-mediated stdio so the engine can
wait indefinitely for permission decisions across hub redeploys —
applies only to Claude. Codex already has the shape we wanted (D3).
That shrinks the bridge wedge's scope and makes it less urgent.

## Consequences

**Becomes possible:**

- Per-tool-call approval gating on Codex stewards from MVP day one,
  feature-parity with Claude's `permission_prompt` (and architecturally
  cleaner, since the JSON-RPC request is deferrable by construction).
- Multi-thread Codex hosting: one app-server process can run several
  agents on the same host, sharing memory rather than paying a fresh
  cold-start per agent. (Not implemented in the first wedge — we'll
  start with one process per agent for symmetry with Claude's spawn
  model — but the protocol supports it when we want it.)
- `turn/steer` and `turn/interrupt` give us a clean kill-switch and
  mid-turn reprompt; lands in a follow-up wedge once the basic flow
  is stable.

**Becomes harder:**

- Two driver shapes to maintain (`driver_appserver.go` JSON-RPC for
  Codex + `driver_stdio.go` stream-json for Claude + `driver_exec.go`
  exec-per-turn for Gemini). Mitigated by the frame-profile system
  carrying the per-vendor translation; the drivers themselves stay
  small and focused on framing.
- Approval-bridge state: parked JSON-RPC request ids must survive
  process restarts to avoid stuck attentions. We persist them in
  `attention_items.pending_payload_json` alongside the existing
  request body. On startup the driver reconciles open requests; if
  the codex side has forgotten them (process exited), we mark the
  attention `failed` and the agent retries on next user input.
- App-server is closed-source-binary territory in the sense that
  upstream changes to the JSON-RPC schema can break us. Mitigation:
  pin to a known-good app-server version per family entry (the
  existing `version_flag` probe machinery from `agent_families.yaml`
  handles this) and run a frame-profile parity test against a recorded
  corpus the same way we do for claude-code's stream-json.

**Becomes forbidden:**

- Reaching for `codex exec --json` for the long-lived agent path.
  It's still appropriate for one-shot use cases (a future
  `plan_executor.go` step that wants a single-prompt LLM call), but
  the steward / worker / sub-agent path goes through app-server.
- Mixing `attention_reply` (the turn-based reply for principal-level
  asks) and JSON-RPC approval responses (the per-tool-call gate). They
  share the same `attention_items` row shape but have different
  delivery paths back to the engine: `attention_reply` becomes a
  user-text turn (ADR-011 D4), JSON-RPC approval responses become a
  JSON-RPC `{result:{decision}}` reply on the stdio pipe. The driver
  routes by attention kind.

## References

- Discussion driving the choice: research conducted 2026-04-29 in this
  session; sources cited inline in conversation log (OpenAI Codex
  App Server docs, gemini-cli headless docs, OpenAI "Unlocking the
  Codex harness" blog, InfoQ Feb 2026 piece).
- Related ADRs:
  - `010-frame-profiles-as-data.md` — the substrate this builds on
  - `011-turn-based-attention-delivery.md` — D6 (`permission_prompt`
    sync exception) is now Claude-only per this ADR's D7
- Implementation (forward-looking; this ADR is the plan, code lands
  in subsequent wedges):
  - `hub/internal/agentfamilies/agent_families.yaml` — codex family
    + frame profile rules keyed on `method` field
  - `hub/internal/hostrunner/driver_appserver.go` — JSON-RPC client,
    thread manager, approval bridge (new file)
  - `hub/internal/hostrunner/launch_m2.go` — per-family
    `writeMCPConfig` dispatch (TOML for codex, JSON for claude)
  - `hub/internal/server/handlers_attention.go` —
    `dispatchAttentionReply` extension to route JSON-RPC approval
    responses on resolved `permission_prompt` attentions
- External documentation:
  - https://developers.openai.com/codex/app-server (protocol reference)
  - https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md
    (transport + auth + schema generation)
  - https://google-gemini.github.io/gemini-cli/docs/cli/headless.html
    (Gemini's exec-per-turn shape, for the contrast in D6)
