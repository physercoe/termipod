---
name: Codex M2 app-server surface audit
description: Source-grounded audit of the codex v2 app-server protocol against the termipod AppServerDriver. Maps every server→client request to current driver coverage (4 bridged, 6 silently auto-declined), corrects last session's "hooks are a bridge surface" misread (they're codex-internal scripts, not a wire-level handler), confirms `experimentalApi=false` rules out two of the gap methods, and catalogues four follow-up wedges (per-server bypass scoping, method-shape coverage, hook observability, Url-mode elicitation) with sizing + verified citations. Deferred — captured for selection in a future session.
---

# Codex M2 app-server surface audit

> **Type:** discussion
> **Status:** Open (2026-05-26) — captures what was learned from reading `openai/codex` `codex-rs/app-server-protocol` and `codex-rs/core/src/hook_runtime.rs`. No implementation in this commit; the 4 wedges listed in §5 are sized but unselected.
> **Audience:** contributors
> **Last verified vs code:** v1.0.712-alpha (hub) + codex-cli `0.133.0` + `openai/codex` `main` HEAD 2026-05-26 (sparse-checkout of `codex-rs/app-server-protocol` + `codex-rs/core` + `codex-rs/protocol` + `codex-rs/app-server`)
> **Freshness:** snapshot (re-verify against `openai/codex@HEAD` if more than 1–2 minor codex releases pass)

**TL;DR.** Reading the upstream protocol crate (made possible by
the OSS discovery in [[discussions/codex-m4-research.md]] §0)
changes the picture set in last session's pause point. Codex's
`v2/hook.rs` is **not** a bridge surface — it describes codex's
internal hook runtime (`HookEventName::PreToolUse`,
`PermissionRequest`, etc.) that codex executes against on-disk
scripts/prompts registered in `~/.codex/config.toml`. The
`hook/started` + `hook/completed` notifications it emits are
observability-only. That means the planned "replace
`AutoAcceptMCPToolCalls` with per-hook routing" rewrite was
mis-framed: per-hook routing would mean installing on-disk hook
scripts that call back into the hub, not extending the driver.

The real surface gap is on the **server→client request side**.
The v2 protocol declares 8 server-initiated request methods (plus
2 v1 legacy); our driver bridges 4 of them and silently
auto-declines the remaining 6 with empty `{}`. Two of the
uncovered methods (`item/tool/call`, `item/tool/requestUserInput`)
are EXPERIMENTAL — gated behind the `experimentalApi=true`
handshake capability, which our driver declares as `false` — so
those should never fire on us. The other four can fire and at
minimum need per-method correct DECLINE shapes to avoid latent
codex-side stall.

This doc audits the surface, fixes the misread, and proposes 6
wedges (5 sized + 1 speculative). The highest-value pick is
**Wedge F** — codex's `account/rateLimits/updated` +
`account/read` notifications expose a richer rate-limit + plan
surface than claude-code's M4 statusLine, and the mobile chips
that landed in ADR-036 Phase B already consume the engine-
agnostic contract. Pick when convenient; not loading anyone
right now.

---

## 1. What the v2 server-request surface is

Upstream source: `codex-rs/app-server-protocol/src/protocol/common.rs:1321`
declares the `ServerRequest` enum via the
`server_request_definitions!` macro. The 10 variants:

| Method | Variant | Shape |
| --- | --- | --- |
| `item/commandExecution/requestApproval` | `CommandExecutionRequestApproval` | v2 exec approval |
| `item/fileChange/requestApproval` | `FileChangeRequestApproval` | v2 patch approval |
| `item/tool/requestUserInput` | `ToolRequestUserInput` | EXPERIMENTAL — user input for a tool |
| `mcpServer/elicitation/request` | `McpServerElicitationRequest` | MCP `elicitation/create` forwarded |
| `item/permissions/requestApproval` | `PermissionsRequestApproval` | sandbox-expansion (network/FS overlay) |
| `item/tool/call` | `DynamicToolCall` | EXPERIMENTAL — codex asks us to execute a client-side tool |
| `account/chatgptAuthTokens/refresh` | `ChatgptAuthTokensRefresh` | OAuth token refresh ping |
| `attestation/generate` | `AttestationGenerate` | upstream attestation snapshot |
| (none — v1 legacy) | `ApplyPatchApproval` | legacy SendUserTurn path |
| (none — v1 legacy) | `ExecCommandApproval` | legacy SendUserTurn path |

Server-initiated requests reach our driver through `readLoop` →
`handleServerRequest` at
`hub/internal/hostrunner/driver_appserver.go:965`. Notifications
go through `translateNotification` at the same file's `:1353`.

Method names traced to `codex-rs/app-server-protocol/src/protocol/common.rs:1325,1332,1338,1344,1350,1356,1361,1367,1375,1381`.

## 2. Hooks are NOT a bridge surface (correcting last session)

The misread that anchored last session's pause point:

> "codex-rs/app-server-protocol/src/protocol/v2/hook.rs declares
> `HookEventName::{PreToolUse, PermissionRequest, …}` — almost 1:1
> with claude-code's hook contract. The upstream change is
> already shipped."
>
> — same-day amendment in [[discussions/codex-m4-research.md]] §0
> (2026-05-25 PM, the OSS-discovery pause note)

What it actually is, traced through source:

- `codex-rs/app-server-protocol/src/protocol/v2/hook.rs:19` defines
  the **event names** (`PreToolUse`, `PermissionRequest`,
  `PostToolUse`, `PreCompact`, `PostCompact`, `SessionStart`,
  `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop`).
- `codex-rs/app-server-protocol/src/protocol/v2/hook.rs:25` defines
  the **handler types** (`Command`, `Prompt`, `Agent`).
- `codex-rs/core/src/hook_runtime.rs` (909 lines) is the actual
  **runtime** — codex's core spawns handlers by exec'ing a shell
  command, running a prompt against its own model, or invoking a
  sub-agent. Imports `codex_hooks::{PreToolUseRequest,
  PostToolUseRequest, PermissionRequestRequest, ...}` from a
  separate `codex-hooks` crate.
- Hooks are **registered on disk**, not over the wire. Operators
  add `[[hooks]]` blocks to `~/.codex/config.toml`; codex
  discovers them at session start.
- The only wire-level surface is observational:
  `HookStartedNotification` (`hook/started`,
  `app-server-protocol/.../v2/hook.rs:141`) and
  `HookCompletedNotification` (`hook/completed`, same file `:147`).
  Both carry a `HookRunSummary` describing what already ran.

So if we wanted "per-hook routing for PreToolUse", the
mechanism would be:

1. Install an executable handler on disk (e.g.
   `~/.codex/hooks/termipod-pretooluse`).
2. Write a `~/.codex/config.toml` `[[hooks]]` entry pointing at
   it, scoped to `PreToolUse`.
3. The handler reads codex's stdin payload (the tool-call about
   to fire), opens a UDS / HTTP back to the host-runner to ask
   for a decision, writes its `BlockOrContinue` JSON back to
   stdout.

That's a fundamentally different shape from the AppServerDriver's
in-process bridge — closer to the M4 claude-code statusLine shim
(`hub/internal/drivers/local_log_tail/claude_code/...`) than to
anything in the driver. It could still happen as a wedge later
(it would be Wedge E in §5 sizing), but it doesn't replace the
existing bypass logic — it complements it.

The v1.0.712 `AutoAcceptMCPToolCalls` bypass is **not** redundant
with that path either: MCP-server elicitations are a separate
flow from codex's own tool calls. PreToolUse hooks fire on
codex's built-ins (`apply_patch`, `exec_command`); the elicitation
path fires when an MCP server downstream of codex requests user
input. They're orthogonal contracts.

## 3. Current driver coverage matrix

Traced from `hub/internal/hostrunner/driver_appserver.go:965-1175`
(`handleServerRequest`, `isApprovalMethod`, `isElicitationMethod`,
`autoDeclineResultFor`):

| Method | Bridge | On-decline shape | Notes |
| --- | --- | --- | --- |
| `item/commandExecution/requestApproval` | ✅ attention `permission_prompt` | `{decision: "decline"}` | `:1102` |
| `item/fileChange/requestApproval` | ✅ attention `permission_prompt` | `{decision: "decline"}` | `:1103` |
| `item/permissions/requestApproval` | ✅ attention `permission_prompt` | `{decision: "decline"}` | `:1104` |
| `mcpServer/elicitation/request` (Form, mcp_tool_call) | ✅ auto-accept on `AutoAcceptMCPToolCalls` | `{action: "accept", content: {}, _meta: {persist: "session"}}` | `:1016`; v1.0.712 |
| `mcpServer/elicitation/request` (Form, real schema) | ✅ attention `elicit` | `{action: "decline"}` | `:1053` |
| `mcpServer/elicitation/request` (Url) | ⚠️ falls through to `elicit` | `{action: "decline"}` | `_meta` URL only surfaces as part of elicitationMessage; rendering is lossy |
| `item/tool/requestUserInput` | ❌ silent `{}` | `{}` | EXPERIMENTAL — gated by `experimentalApi=true`; our handshake declares `false` (`:289`), so dormant in practice |
| `item/tool/call` | ❌ silent `{}` | `{}` | EXPERIMENTAL — same gate; dormant |
| `account/chatgptAuthTokens/refresh` | ❌ silent `{}` | `{}` | Can fire on ChatGPT-authed accounts; on API-key accounts dormant |
| `attestation/generate` | ❌ silent `{}` | `{}` | Can fire any time codex wants a fresh attestation snapshot |
| `applyPatchApproval` (v1) | ❌ silent `{}` | `{}` | Only fires on legacy SendUserTurn / SendUserMessage paths; our driver uses `turn/start` (`:302`) |
| `execCommandApproval` (v1) | ❌ silent `{}` | `{}` | Same — dormant |

"Silent `{}`" means `handleServerRequest:990` writes `{}` as the
response. Codex's `serde_json::from_value::<…Response>(…)`
deserialize will fail with a missing-field error; the failure
mode is method-specific (some stall the turn, some skip and log).

Notifications coverage is broader — translation lives at
`:1353-1444` — but we skip many. Not audited in this pass.

## 4. The five real gaps

After ruling out the experimental + v1 dormant methods, the
live surface area for improvement is narrow. Four are
defensive corrections to existing coverage; the fifth (§5-F)
is the highest-value addition — it unlocks claude-code-M4
parity for codex steward telemetry chips that already exist
mobile-side.

1. **Per-server bypass scoping** (v1.0.712 lacuna). The
   `AutoAcceptMCPToolCalls` flag is set per-driver based on
   `approval_policy=never`, then applied uniformly to every
   `mcp_tool_call` elicitation regardless of `serverName`. The
   hub's own MCP server is fully trusted, but operators can wire
   up arbitrary third-party MCP servers via codex config. Those
   third-party servers shouldn't silently bypass the gate.
2. **`attestation/generate` decline shape**. If it ever fires,
   codex deserializes our `{}` against
   `AttestationGenerateResponse`, fails, propagates the error
   into the agent transcript. Defensive coverage; we don't know
   the trigger condition because the spawn path is buried in
   `bespoke_event_handling.rs`.
3. **`account/chatgptAuthTokens/refresh` decline shape**. Same
   problem class. Operators using a ChatGPT-authed codex
   (versus API-key) would hit it on token rotation. We have at
   least one user on `default_claude_max_5x` who runs codex with
   ChatGPT auth on the principal device — this can fire.
4. **`hook/started` / `hook/completed` notifications**. Currently
   skipped by `translateNotification`. Pure observability — when
   operators install custom codex hooks, they fire silently.
   Surfacing them as system events makes hook debugging possible
   from the transcript.
5. **`account/rateLimits/updated` + `account/updated` +
   `account/read` + `account/rateLimits/read`** — completely
   untranslated. Codex carries a richer rate-limit + plan-type
   + credits-balance surface than claude-code's M4 statusLine;
   the ADR-036 mobile chips already consume the engine-agnostic
   shape; we just don't map codex's `primary`/`secondary`
   `RateLimitWindow` into `5h_window`/`weekly_window` on the
   hub side. Highest-value gap. Detail in §5-F.

## 5. Wedge catalogue (sized, unselected)

### A — Per-server bypass scoping (~100 LOC + 3 tests)

Replace `AutoAcceptMCPToolCalls bool` with
`AutoAcceptMCPToolCallsFromServers []string` (default
`["hub"]`). In `handleServerRequest:1016`, gate auto-accept on
`stringPath(params, "serverName")` being in the set. Cards still
raise for unknown servers even in bypass mode.

Wire from `launch_m2.go`: when `codexApprovalPolicy() == "never"`,
seed the set from a new YAML key under the codex profile (default
`["hub"]`); operators can extend the list per-spawn.

**Test scope:** hub-allowlist accept, third-party-deny, missing
`serverName` (defensive — treat as untrusted), and an existing
`TestAppServerDriver_AutoAcceptMCPToolCallsBypassesAttention`
update to match the new field shape.

**Risk:** the only currently-known MCP server we hook through
codex is the hub's; this is forward-looking and reduces an
attack surface that doesn't exist yet on any deployed system.
Reasonable to ship before that surface exists, given the cost
of retrofit if a user adds an untrusted MCP server first.

### B — Method-shape coverage (~80 LOC + 5 tests)

Extend `autoDeclineResultFor(method)` to cover `attestation/generate`
and `account/chatgptAuthTokens/refresh` (likely both decline with
`{error: "..."}`-style shapes — confirm against
`AttestationGenerateResponse` and `ChatgptAuthTokensRefreshResponse`
in `app-server-protocol`). v1 `applyPatchApproval` /
`execCommandApproval` get explicit `{decision: "deny"}` shapes for
defensive coverage even though they're dormant.

**Test scope:** one decline-shape test per method; verify codex's
own response-deserialize succeeds against the produced shape (if
we have a way to round-trip via the protocol crate — likely
literal JSON-shape match against the upstream type's serde
attrs).

**Risk:** low — pure defensive. The unknowns (attestation /
auth-refresh response shapes) are 5-minute lookups in the
upstream crate.

### C — Hook observability (~60 LOC + 2 tests)

In `translateNotification`, add cases for `hook/started` and
`hook/completed`. Emit a system event with `kind:
codex_hook_started` / `codex_hook_completed` carrying
`{event_name, handler_type, source_path, status, duration_ms,
entries[]}` from the `HookRunSummary`. Surface in the mobile
agent feed under existing system-event rendering.

**Test scope:** synthetic notification → expected agent_event
shape; status=failed propagates `status_message`.

**Risk:** very low — pure additive observability.

### D — Url-mode elicitation card (~40 LOC + 1 test)

In `handleServerRequest:1050-1054`, distinguish
`McpServerElicitationRequest::Form` from `::Url` by checking
`params.mode` (or whichever discriminator codex exports — see
`app-server-protocol/src/protocol/v2/mcp.rs:617`'s
`#[serde(tag = "mode")]`). Emit attention with a richer kind
(`elicit_url`) carrying the URL prominently so the principal sees
the destination they're authorising before clicking through.

**Test scope:** Url-mode params → attention with URL surfaced;
Form-mode unchanged.

**Risk:** low — additive. Mobile-side rendering may need a small
follow-up.

### E — On-disk hook bridge (NOT sized; speculative)

If we ever want true per-tool-call routing from codex's PreToolUse
hook into the hub: install a binary at
`~/.codex/hooks/termipod-pretooluse`, plus
`~/.codex/config.toml` `[[hooks]]` overlays. Mirrors the
claude-code statusLine shim pattern from v1.0.696-698. NOT a
small wedge — needs design, distribution story for the binary,
config-merge ergonomics (wrap operator's existing hook entries
under a `_termipod_wrapped_<event>` marker block + invoke via a
`--wrap` shim flag; mirrors the claude-code statusLine install
pattern from v1.0.696-698).

Listed here so the option doesn't vanish; not a near-term ask.

### F — Rate-limit + account telemetry parity with claude-code M4 (~150 LOC + 8 tests)

**Highest-value wedge in this catalogue** — codex's v2 protocol
exposes a richer rate-limit + account-state surface than
claude-code's M4 statusLine, but our driver doesn't translate
any of it. The mobile chips that landed in ADR-036 Phase B
(v1.0.700-703) consume `usage.rate_limits.{5h_window,
weekly_window}` from session telemetry — engine-agnostic by
design — so once the hub maps codex's shape into the same
contract, every chip renders without mobile changes.

**Mechanism — wire-native, no shim install.** The data path is
strictly the JSON-RPC pipe `AppServerDriver` already owns;
nothing leaves the existing stdio connection:

| | claude-code M4 (v1.0.696-698) | codex M2 (this wedge) |
| --- | --- | --- |
| Data export interface | `~/.claude/settings.local.json::statusLine.command` — a shell command claude invokes every ~10s with JSON on stdin | first-class JSON-RPC notification on the active connection |
| Shim binary | required (`hub/internal/hostrunner/statusline_shim/`) | none |
| UDS gateway | required (`hub/internal/hostrunner/uds_gateway.go`) | none |
| Config merge | wrap-and-passthrough into operator's existing statusLine | none |
| Total scope shipped | ~760 LOC + 16 tests | ~150 LOC + 8 tests |

Inside the codex process, codex makes its own API calls, sees
rate-limit headers in the responses, and maintains a
`RateLimitSnapshot` in process memory. We never touch the API
and never need OAuth tokens or ChatGPT creds. Codex hands us
that snapshot two ways on the stdio pipe we're already reading:

- **client→server pull** — we `d.Call(ctx, "account/rateLimits/
  read", nil)` once at handshake. Same `client_request`
  protocol-macro block as `thread/start`, `thread/resume`,
  `fs/readFile` (`common.rs:923`) — methods we already call.
- **server→client push** — codex emits `account/rateLimits/
  updated` whenever the snapshot changes. Same notification
  macro block as `thread/started`, `turn/started`,
  `hook/started` (`common.rs:1517`) — notifications we already
  read in `translateNotification`.

The reason: codex was designed app-server-first (the JSON-RPC
protocol IS its public surface, so `RateLimitSnapshot` is
first-class on the wire); claude-code was TUI-first with hooks
retrofitted, so `statusLine` is the only export plug and we
had to install a shim through it.

**On the wire** (cited):

| Surface | Method/Notification | Source |
| --- | --- | --- |
| Initial snapshot fetch | `account/rateLimits/read` | `protocol/common.rs:923` |
| Live updates | `account/rateLimits/updated` | `protocol/common.rs:1517` |
| Account state fetch | `account/read` | `protocol/common.rs:1029` |
| Account-state push | `account/updated` (`AccountUpdatedNotification`) | `app-server-protocol/.../v2/account.rs:242` |

**`RateLimitSnapshot`** (`account.rs:257`) carries:
`{limit_id, limit_name, primary: RateLimitWindow,
secondary: RateLimitWindow, credits: CreditsSnapshot,
plan_type, rate_limit_reached_type}`. **`RateLimitWindow`**
(`account.rs:337`) is
`{used_percent, window_duration_mins, resets_at}`.

**Shape mapping to existing mobile contract:**

| claude-code statusLine field | codex equivalent | Notes |
| --- | --- | --- |
| `rate_limits.5h_window.used_percent` | `primary.used_percent` | direct |
| `rate_limits.5h_window.windowMinutes` | `primary.window_duration_mins` | direct |
| `rate_limits.5h_window.resets_at` | `primary.resets_at` | direct (Unix epoch ms) |
| `rate_limits.5h_window.used` | (absent) | codex is percent-only; mobile chip should fall back to `"NN%"` when raw absent |
| `rate_limits.weekly_window.*` | `secondary.*` | same fields |
| `usage.model` | `turn_context.model` | already in JSONL — separate path |
| `oauthAccount.organizationRateLimitTier` | `plan_type` + `credits.balance` | richer than claude — explicit plan + dollar balance |
| (none) | `rate_limit_reached_type` | bonus — semantic "why" (rate-limited / credits depleted / workspace-owner-vs-member usage-limit) |

**Implementation scope:**

1. **Handshake** (`driver_appserver.go:278`): after
   `initialize` + `thread/start`, also `Call(ctx,
   "account/read", {})` + `Call(ctx, "account/rateLimits/read",
   {})`. Stamp the responses into the existing `session.init`
   + first `usage` agent_events the mobile already reads.
2. **Notification translation** (`driver_appserver.go:1353`):
   add `account/rateLimits/updated` + `account/updated` cases.
   Emit the existing `usage` event kind; populate
   `rate_limits.5h_window` / `rate_limits.weekly_window` from
   `primary`/`secondary`; surface `credits` + `plan_type` +
   `rate_limit_reached_type` as new optional fields on `usage`
   (mobile ignores unknown fields).
3. **Codex frame profile** (`agentfamilies/agent_families.yaml`):
   JSONPath extractors mirroring the claude-code statusLine
   mapping (the `last_*` per-turn extractors from v1.0.712 are
   the closest precedent — same `notification`-driven shape).
4. **Mobile** — no change needed for chip rendering. Optional
   follow-up wedge surfaces `credits.balance` + `rate_limit_
   reached_type` once the operator confirms the parity baseline
   works.

**Test scope:** hub-only — 1 handshake-pair test
(account/read + rateLimits/read decorate session.init), 4
notification-translation tests (rate-limit, account-updated,
both-windows-present, only-primary-present), 3 chip-mapping
tests (RateLimitWindow → 5h_window shape with raw
`used`-absent fallback).

**Risk:** low — additive, engine-agnostic contract on the
mobile side is already proven (claude-code M4 ships it). The
unknowns are minor (do we get `resets_at` in ms or s? codex
declares `i64` without unit; verify empirically before chip
labels). Same class of work as v1.0.712.

**Why this is highest-priority:** every claude-code M4
statusLine affordance the operator likes has a direct codex
equivalent on the wire that we discard. Wedges A-D are
defensive corrections; Wedge F is genuine new capability for
the codex steward UX.

## 6. What this doc deliberately doesn't do

- Pick a wedge. The user explicitly deferred: "doc these into
  discussion, we won't do these right now."
- Audit notification translation completeness. The skipped-
  notification surface in `translateNotification` is large; a
  separate pass would map every v2 notification (1472-onwards in
  `common.rs`) against driver coverage.
- Verify exact response shapes for the gap methods. §5-B notes
  these as 5-minute lookups; they happen at implementation time,
  not now.
- Touch the M4 wedge plan. [[discussions/codex-m4-research.md]]
  §0 already paused that — same justification applies (M2 surface
  is more accessible than the M4 schema work, given the protocol
  crate is readable).

## 7. Related

- [[discussions/codex-m4-research.md]] §0 — the OSS discovery
  that unlocked reading the protocol source.
- [[decisions/012-codex-app-server-integration.md]] — original
  ADR establishing the M2 driver; gaps in §3 of this doc map to
  ADR-012 follow-ups.
- `hub/internal/hostrunner/driver_appserver.go:965-1175` —
  current server-request handler + decline plumbing.
- `hub/internal/hostrunner/driver_appserver.go:1353-1444` —
  notification translation (gap for Wedge F + C).
- `codex-rs/app-server-protocol/src/protocol/common.rs:1321-1385`
  — upstream `server_request_definitions!` macro invocation.
- `codex-rs/app-server-protocol/src/protocol/v2/account.rs:181-353`
  — upstream `GetAccountRateLimitsResponse` +
  `AccountRateLimitsUpdatedNotification` + `RateLimitSnapshot`
  + `RateLimitWindow` (Wedge F shape source).
- `codex-rs/core/src/hook_runtime.rs` — upstream hook execution
  runtime (proof that hooks are codex-internal).
- [[decisions/036-claude-code-statusline-telemetry.md]] +
  [[plans/claude-code-statusline-as-telemetry.md]] — the engine-
  agnostic mobile chip contract Wedge F adopts.
