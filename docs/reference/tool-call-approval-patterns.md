# Tool-call approval patterns

> **Type:** reference
> **Status:** Current (2026-05-17)
> **Audience:** contributors
> **Last verified vs code:** v1.0.619-alpha

**TL;DR.** Three distinct patterns deliver "agent wants to call a
tool that needs approval, principal answers" today. Two of them are
async (turn-based on the wire), one is sync (MCP call holds open).
Which one fires depends on the engine + the verb the agent invoked,
not on a global config knob. All three converge on the same
`attention_items` + `/decide` flow, which means a single routing
extension (today: none) would affect all three at once.

---

## 1. The three patterns

| Pattern | Trigger | Block model | Engines | Canonical file |
|---|---|---|---|---|
| **A — sync MCP hold-open** | Engine calls `permission_prompt` MCP tool as part of its `--permission-prompt-tool` hook contract | Hub holds the MCP call open for up to 10 min (`permissionPromptTimeout`) via `waitForAttentionDecision` | claude-code | `hub/internal/server/mcp_more.go:1026-1190` |
| **B — async parked JSON-RPC** | Codex app-server sends an approval request on its long-lived stdio JSON-RPC pipe (`applyPatch` / `commandExecution` / `permissions.requestApproval` / `mcpServer/elicitation/request`) | Driver parks the JSON-RPC id in `pendingApprovals` and returns from the hub MCP call immediately; codex's request stays parked on the wire until `/decide` lands and the driver writes the response back | codex | `hub/internal/hostrunner/driver_appserver.go:137-582` |
| **C — async via `request_*` family** | Agent prompt explicitly calls `request_approval` / `request_select` / `request_help` / `request_project_steward` / `elicit` | MCP returns `{status:"awaiting_response"}` immediately; agent ends its turn; `/decide` fans an `input.attention_reply` event delivered as a fresh user turn on the agent's next pickup | all engines (driven entirely through hub MCP + agent_events) | `hub/internal/server/mcp_more.go:430-605`, `handlers_attention.go:648-713` |

---

## 2. Pattern A — sync MCP hold-open (Claude `permission_prompt`)

**Why sync.** claude-code's `--permission-prompt-tool` is a
synchronous `canUseTool` hook. Claude calls it **before** the tool
runs and expects an immediate `{behavior: "allow" | "deny"}` answer
before letting the tool execute. There's no parked-request channel
on Claude's wire, so the MCP call has to hold open.

**Flow.**
1. Worker launched with `--permission-prompt-tool mcp__termipod__permission_prompt` (when `permission_mode=prompt`; default since v1.0.617 is `skip`).
2. Worker decides to call a tool (e.g. Write).
3. claude-code calls `permission_prompt(tool_name, input, tool_use_id)` first.
4. `mcpPermissionPrompt` (`mcp_more.go:1045`):
   - **Tier auto-allow** (`mcp_more.go:1094-1107`): `tierFor(tool_name)` returns `trivial` / `routine` → return `{behavior:"allow"}` immediately + write `permission_prompt.auto_allowed` audit row. Read, Glob, web search, journal_append etc. take this path.
   - **Escalation**: write `attention_items` row (`kind='permission_prompt'`, severity=`minor`), then `waitForAttentionDecision(pctx, id)` (`mcp_more.go:1159-1178`). The MCP call blocks until either:
     - someone calls `/decide` → returns `{behavior:"allow", updatedInput}` or `{behavior:"deny"}`
     - 10-min timeout → row is force-resolved + return `{behavior:"deny", message:"no decision within timeout"}` (fail-closed)
5. claude proceeds with the tool call (or skips on deny) and continues the turn.

**Anti-pattern from v1.0.617 fix.** If the spawn cmd expands to no
permission flag at all (`{{permission_flag}}` empty), claude-code
denies destructive tools without ever calling `permission_prompt`.
The worker chat shows *"pending your approval"* and no
attention_item is created. v1.0.617 rewrote empty `mode → "skip"`
in `backendVarsFromSpec` to prevent this; the bundled steward
templates and mobile sheet had already been passing explicit
`"skip"` so only the MCP `agents.spawn` path silently hit it.

---

## 3. Pattern B — async parked JSON-RPC (Codex app-server)

**Why async.** Codex speaks JSON-RPC over a long-lived stdio pipe.
Each request carries a unique `id` and codex doesn't block waiting
for a response on that id — it can issue other requests or accept
incoming events. The driver can park the request indefinitely and
respond when ready.

**Flow.**
1. Codex sends e.g. `{id: 42, method: "applyPatch", params: {...}}` on stdio.
2. `AppServerDriver.bridge` translates the request into a hub-side `permission_prompt` attention (same row shape as Pattern A) and records `pendingApprovals[attention_id] = jsonRPCID` (`driver_appserver.go:137-139`).
3. The MCP call returns from the hub immediately. Codex's `id=42` request is now parked.
4. When `/decide(approve)` lands → `dispatchAttentionReply` (`handlers_attention.go:648`) writes `kind='input.attention_reply'` event into the agent's session.
5. The driver's `attention_reply` handler (`driver_appserver.go:408-432`) looks up the parked id by attention_id and writes a JSON-RPC response back on stdio. Per-method response shaping (`driver_appserver.go:505-566`):
   - `applyPatch` / `commandExecution` → `{decision: "accept"|"reject"}`
   - `permissions.requestApproval` → `{scope:"turn", permissions:{}}`
   - `mcpServer/elicitation/request` → `{action:"accept", content:{}, _meta:{persist:"session"}}`
6. Codex receives the response and proceeds.

**Why the comment in `handlers_attention.go:436-438` matters.**
Both Pattern A and Pattern B raise rows with `kind='permission_prompt'`.
The dispatch allowlist at line 436 must include `permission_prompt`
specifically so the codex case fans `input.attention_reply` to wake
its driver — even though the claude case never reaches
`dispatchAttentionReply` (claude resolves via the sync MCP return
in step 4 above, not through the event stream).

---

## 4. Pattern C — async via `request_*` family

**Why async.** These verbs are designed to model "agent yields a
turn to the principal." The MCP call returns immediately so the
agent's turn can end cleanly; the reply comes back as a fresh user
turn on the next pickup. The principal-attention round-trip is
turn-based all the way through.

**The verb family.**
| Verb | Shape | File |
|---|---|---|
| `request_approval(question)` | yes/no | `mcp_more.go:430-475` |
| `request_select(question, options)` | pick one of N | `mcp_more.go:477-530` |
| `request_help(question, mode)` | free-text reply; mode = clarify / handoff / fill | `mcp_more.go:532-605` |
| `request_project_steward(project_id, reason, suggested_host_id)` | ADR-025 W4 — director materializes a project steward | `mcp_more.go:552-605` |
| `elicit` (codex MCP server) | schema-driven form | (codex-side wire shape; hub treats as permission_prompt variant) |

**Flow.**
1. Agent calls e.g. `request_help(question="should we use sklearn or scipy?")`.
2. MCP returns `{id, kind:"help_request", status:"awaiting_response", requested_by}` immediately.
3. Agent ends its turn (the agent prompt should say "END YOUR TURN AFTER CALLING" — every steward template has this).
4. Hub creates `attention_items` row with `kind='help_request'`, `session_id` stamped from `lookupAgentSession(fromID)` so reply routing works.
5. Principal answers via `/decide(approve, body="<reply>")`.
6. `dispatchAttentionReply` writes `kind='input.attention_reply'` into the agent's session.
7. Host-runner's `InputRouter` delivers the event as a fresh user-turn input → agent picks up where it left off on next turn.

**Constraint.** Body required on `approve` for `help_request`
(`handlers_attention.go:315-319`) — an empty approve is meaningless;
`reject` is fine and is the "drop it" dismissal path.

**Project-steward-request fan-back fixed v1.0.612-alpha.** The
allowlist originally only included `approval_request | select |
help_request | permission_prompt | elicit`; `project_steward_request`
was missing, so general-steward `request_project_steward` calls
resolved silently and the steward parked forever. v1.0.612 added
`project_steward_request` to the allowlist.

---

## 5. What's common to all three

| Property | All three patterns |
|---|---|
| Backing row | `attention_items` (one row per ask) |
| Resolution surface | `POST /v1/teams/{team}/attention/{id}/decide` |
| Decision storage | `decisions_json` array, append-only |
| Quorum | `policy.QuorumFor(tier)`; reject always resolves; approves accumulate to threshold |
| Auto-derive | `tierFor(tool_name)` short-circuits trivial/routine reads in Pattern A only — Patterns B and C do not have a built-in tier filter |
| Audit | `permission_prompt.request` / `permission_prompt.auto_allowed` / `attention.decide` |
| Notification | Whatever sits on the principal-attention inbox surface (mobile Me page). No engine-specific notification path. |

Because the storage + decide layer is uniform, **a single routing
extension at the `attention_items` row level (e.g. addressing it to a
specific agent like a project steward) would change all three
patterns at once**. See `docs/discussions/worker-permission-routing-to-steward.md`
for the open design question.

---

## 6. Which pattern fires when?

This is the part that surprises people. There's no global switch —
the pattern is determined by *what the agent calls* and *what engine
it's running*:

| Agent verb (what was called) | Engine | Pattern |
|---|---|---|
| Engine-native tool (Write, Bash, apply_patch, …) | claude-code | A (sync) |
| Engine-native tool | codex | B (async parked) |
| Engine-native tool | gemini-cli, kimi-code | currently no permission gate wired; engines run with their own native bypass flags |
| `request_help` / `request_approval` / `request_select` | any | C (async turn-based) |
| `request_project_steward` | any | C (async turn-based, fan-back fixed v1.0.612) |
| `permission_prompt` (called directly) | n/a — claude-code calls this internally; agent prompts don't call it themselves | A |
| Codex `mcpServer/elicitation/request` | codex | B (driver routes through pendingApprovals) |

---

## 7. Where to look in the code

- **Pattern A core**: `hub/internal/server/mcp_more.go:1025-1200` (`mcpPermissionPrompt`)
- **Pattern A tier filter**: `hub/internal/server/tiers.go` (`tierFor`)
- **Pattern A timeout**: `hub/internal/server/mcp_more.go:1043` (`permissionPromptTimeout = 10 * time.Minute`)
- **Pattern B parking**: `hub/internal/hostrunner/driver_appserver.go:137-150` (`pendingApprovals`)
- **Pattern B reply routing**: `hub/internal/hostrunner/driver_appserver.go:408-582` (`attention_reply` → `resolvePendingApproval`)
- **Pattern B per-method response shapes**: `driver_appserver.go:505-566`
- **Pattern C verbs**: `hub/internal/server/mcp_more.go:430-605`
- **Common fan-back**: `hub/internal/server/handlers_attention.go:648-713` (`dispatchAttentionReply`)
- **Fan-back allowlist** (which kinds get fanned vs which don't): `handlers_attention.go:436-447`
- **Decide endpoint**: `handlers_attention.go:264-451` (`handleDecideAttention`)

---

## 8. Open design questions linking back to this doc

- [Worker permission routing to project steward](../discussions/worker-permission-routing-to-steward.md) — should worker tool-call approvals route to the project's steward (the agent that owns the project's spawn authority) instead of the principal? Today all three patterns land in the team-wide attention inbox with no addressing; principal is the de facto approver. Would be one row-level extension affecting all three patterns at once.
