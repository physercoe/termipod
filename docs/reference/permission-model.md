# Permission model

> **Type:** reference
> **Status:** Current (2026-05-01)
> **Audience:** contributors · template authors · operators
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Termipod's permission model has **three modes** for routine engine tool calls and **separate machinery** for the strategic / asynchronous attention kinds (`request_approval`, `request_select`, `request_help`). The three modes are **default** (engine-native gate, no termipod involvement), **prompt** (engine routes per-call decisions through termipod's MCP-bridged `permission_prompt`), and **dangerously-skip** (auto-allow everything, no gate). The model is shaped by a deliberate design principle — *routine tool calls auto-allow; only strategic decisions surface attention* — and constrained by **vendor contract asymmetry**: each engine's per-call gate has a different shape, only some support deferred approval, and adding a new engine starts here.

---

## Two layers, distinct mechanisms

| Layer | What it gates | Mechanism | Latency |
|---|---|---|---|
| **Tool-call gate** (this doc's scope) | Each individual tool call by the agent (Bash, Edit, Read, …) | Per-engine; either engine-native or routed through termipod's MCP `permission_prompt` | Synchronous — agent waits for the call's answer |
| **Attention gate** | Strategic / async decisions (`request_approval`, `request_select`, `request_help`) | Hub stores the attention; principal answers via mobile; reply delivered as a fresh user turn | Asynchronous — seconds to days; see [ADR-011](../decisions/011-turn-based-attention-delivery.md) |

The two layers are *not* interchangeable. A long-running approval ("ratify this plan") belongs in the attention layer because it can take days; a per-call gate ("can the agent run `rm`?") belongs in the tool-call gate because the agent has to wait for an answer to proceed. Conflating them causes the long-poll bugs ADR-011 §Context describes.

This file documents the tool-call gate. ADR-011 + `discussions/attention-interaction-model.md` document the attention gate.

---

## Design principle: auto-allow routine, surface strategic

> *"claude can use common tool just as user use claude on a pc"* — director, 2026-04-25

Routine tool calls (Read, Bash, Edit, Write, WebSearch, WebFetch, engine-internal `Task`) are **not** something the director should be asked about each time. The director's role is to direct, not operate (ADR-005). A director who has to ratify every Bash call is operating, not directing.

Strategic decisions — approve a plan, pick between options, ask for help — are *exactly* what the director should be asked about. Those go through the attention gate.

The three modes below differ in **how** routine tool calls are gated: by the engine itself (default), by the hub via MCP (prompt), or not at all (dangerously-skip). All three respect the principle by leaving strategic decisions to the attention gate.

---

## Mode 1 — `default` (engine-native gate)

The engine's own `canUseTool` callback (or equivalent vendor mechanism) decides per-call. Termipod does **not** intervene.

- **Claude code.** The engine's built-in tool-permission UI prompts in the engine's own session. Termipod's hub never sees the request.
- **Codex.** The engine's app-server JSON-RPC `permission_request` notifications resolve via the engine's terminal UI (when running interactively); when running headless under termipod, the engine errors or auto-rejects depending on configuration.
- **Gemini-cli.** The engine's `--yolo` flag controls auto-allow; without it, the engine prompts in its own UI.

**When to use.** Local development, hands-on operator-style use where the principal is at the engine's terminal. Not the standard termipod path — termipod's pitch is mobile direction, which means the engine's UI is not in front of the user.

**Configuration.** Template's `backend.permission_mode: default` (or absent). No flags appended to the engine CLI.

## Mode 2 — `prompt` (MCP-bridged via termipod)

Engine is launched with the appropriate flag to route per-call permission requests through an MCP tool that termipod hosts. Each tool call becomes a `permission_prompt` MCP call to the hub. The hub policy decides allow/deny synchronously.

- **Claude code.** Launched with `--permission-prompt-tool mcp__termipod__permission_prompt`. The hook protocol returns `{behavior: "allow" | "deny"}` synchronously; no deferred branch (vendor contract — see ADR-011 D6).
- **Codex.** Launched with the equivalent app-server bridge; codex's JSON-RPC protocol *does* allow deferred per-tool-call approval, so the hub can in principle hold the request open for an attention-style answer. As of v1.0.350-alpha the hub treats codex's bridge synchronously to match claude's behavior; deferred per-call approval is post-MVP work (ADR-012 D7).
- **Gemini-cli.** Engine has no equivalent in-stream approval gate. Mode 2 is not available for gemini today; it falls back to mode 1 default plus engine-side `--yolo`. New PR-level support in gemini-cli would unblock this.

**When to use.** Mode 2 is the right choice when the operator wants policy-driven gating without the director having to answer each call (the hub policy decides). Useful for hardening: a worker template with a tight allowlist + mode 2 means out-of-allowlist calls deny silently, no human round-trip.

**Configuration.** Template's `backend.permission_mode: prompt`. Engine CLI gets the bridge flag automatically per the engine driver.

**Vendor contract asymmetry.** Each engine's per-call gate has a different shape. Adding a new engine to mode 2 means picking the right vendor protocol, mapping it to MCP, and accepting whatever sync/deferred latitude the vendor allows. Without this asymmetry being explicit, future engine integrations will silently regress to "just port what claude does," which is wrong for engines whose protocols can do better.

| Engine | Per-call gate | Sync / deferrable | Hub-side support today |
|---|---|---|---|
| Claude code | `canUseTool` hook → `--permission-prompt-tool` MCP | **Sync only** (vendor contract — ADR-011 D6) | Yes (canonical) |
| Codex | `app-server` JSON-RPC `permission_request` notifications | Sync + deferrable | Sync only today (ADR-012 D7); deferred = post-MVP |
| Gemini-cli | None (only `--yolo` global toggle) | N/A | No (mode 2 unavailable) |

## Mode 3 — `dangerously-skip` (auto-allow everything)

Every routine tool call is auto-approved. No gate. The agent runs the same way it would if the operator had pressed "Always allow" on every prompt.

- **Claude code.** Launched with `--dangerously-skip-permissions`.
- **Codex.** Equivalent codex flag (`--dangerously-skip-approvals` or per-version equivalent).
- **Gemini-cli.** Launched with `--yolo`.

**When to use.** The default mode for **stewards** (which need broad authoring scope) and for **workers** running under a tight `tool_allowlist` (the template-level allowlist is the gate). Termipod's strategic decisions still surface as attention items (`request_approval`, etc.) — the dangerously-skip flag governs only the engine's per-call routine gate.

**Why "dangerously" is acceptable here.** The flag's name comes from a single-user-with-credentials threat model (the engine could `rm -rf` the user's machine). Termipod adds two structural mitigations:

1. **Operation scope manifest** ([ADR-016](../decisions/016-subagent-scope-manifest.md)). Every `hub://*` tool call is gated by role at the hub MCP boundary, regardless of the engine's tool-call permission mode. A worker can't escalate to `agents.spawn` even with mode 3.
2. **Sandbox** (post-MVP). For the unsigned filesystem mutations (Bash, Edit, Write), the planned mitigation is a per-host bwrap/Seatbelt sandbox + egress proxy. Today these are *deferred* — see `project_post_mvp_sandbox.md` memory.

So mode 3 is "dangerous" in the sense the engine name suggests, but the worst-case blast radius in termipod is bounded by ADR-016 + the host's working directory + (post-MVP) the sandbox.

**Configuration.** Template's `backend.permission_mode: dangerously-skip`. Engine CLI gets the dangerously-skip flag.

---

## Mode selection — the table

| Template kind | Default mode | Reason |
|---|---|---|
| `steward.general.v1` | `dangerously-skip` | Concierge needs broad authoring scope; ADR-016 D6 + D7 are the structural gate. |
| `steward.<domain>.v1` | `dangerously-skip` | Domain stewards do orchestration, not IC; same as above. |
| `*-worker.v1` (ml-worker, lit-reviewer, coder, etc.) | `dangerously-skip` | Workers run under tight `tool_allowlist` + ADR-016 D2 worker surface. The allowlist is the gate. |
| Director-authored ad-hoc agent | operator's choice | If unsure, start with `prompt` to see what the agent tries to do, then move to `dangerously-skip` once trusted. |

These are defaults. The template `backend.permission_mode` field overrides per-template.

---

## Adding a new engine — what to wire

1. **Pick the per-call gate primitive.** Read the vendor docs for the equivalent of claude's `canUseTool` hook. If none exists (gemini today), only mode 1 + mode 3 are available; mode 2 needs vendor work.
2. **Wire the bridge flag.** In the engine driver under `hub/internal/hostrunner/driver_*.go`, append the right CLI flag(s) when `template.backend.permission_mode == prompt` or `dangerously-skip`.
3. **Map the protocol.** If the engine's per-call gate is sync (claude), map directly to a sync MCP `permission_prompt`. If it can defer (codex), decide whether the hub holds the request open or returns sync immediately.
4. **Document the asymmetry.** Update the table above with the new engine's row. If sync-only, note it in an ADR (ADR-011 D6 is the model).
5. **Test the three modes.** A driver test should cover default (no flag), prompt (bridge flag), dangerously-skip (skip flag).

---

## Common pitfalls

**Routing routine calls through the attention gate.** Don't do it. Routine tool calls are sync; attention is async. The mismatch produces the long-poll bug class ADR-011 fixed.

**Assuming all engines support deferred per-call approval.** Claude doesn't. Designs that require deferral for routine calls won't work uniformly across engines. If you need a deferral-style decision, model it as an attention kind, not a tool-call gate.

**Mixing mode 3 with a missing `tool_allowlist`.** A worker on `dangerously-skip` with no allowlist + ADR-016 worker role still has a wide surface (everything in the worker tool table). That's intentional — the role gate is the structural floor — but for a new worker template, set a tight allowlist as a defense-in-depth measure.

---

## References

- [ADR-005](../decisions/005-owner-authority-model.md) — director-as-principal framing.
- [ADR-011](../decisions/011-turn-based-attention-delivery.md) — attention gate (the *other* layer); D6 sidebar covers sync `permission_prompt`.
- [ADR-012](../decisions/012-codex-app-server-integration.md) — codex's per-call protocol; D7 = deferred-approval post-MVP work.
- [ADR-013](../decisions/013-gemini-exec-per-turn.md) — gemini's lack of in-stream gate; D4.
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — `hub://*` tool gate that's orthogonal to (and stronger than) any engine permission mode.
- [Reference: steward-templates](steward-templates.md) — `backend.permission_mode` field in template YAML.
- Code: `hub/internal/hostrunner/driver_stdio.go` (claude), `driver_codex.go`, `driver_gemini.go`.
