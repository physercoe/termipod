# Rate limiting

> **Type:** reference
> **Status:** Current (2026-05-01)
> **Audience:** contributors · operators
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Termipod has **two distinct rate-limit concepts**: **(a) engine vendor quotas** that the engine itself enforces (Anthropic plan limits, OpenAI tokens/min, Google quotas), passed through to the hub via stream-json events and displayed to the principal but never enforced by termipod; and **(b) hub-level governance limits** on agent operations (spawns, tool calls, A2A invocations). Hub-level enforcement is **scope-not-budget** in MVP — see [ADR-016](../decisions/016-subagent-scope-manifest.md). This file documents both: what the principal sees, where it comes from, why hub-level token-bucketing is deferred.

---

## (a) Engine vendor quotas (display-only, not enforced)

Termipod displays vendor rate-limit signals in the agent feed so the principal can see how much budget remains and when the next window resets. The hub does not enforce these limits — only the vendor does. If the engine refuses a tool call because the vendor said "you're over your limit," that's an engine-side error surface, not a hub-side gate.

### Source: claude-code stream-json `rate_limit_event`

The current claude-code SDK emits one of three frame shapes (driver normalizes all three to a single `rate_limit` agent_event):

```jsonc
// older SDKs: flat
{"type":"rate_limit_event", "rateLimitType":"five_hour", "status":"allowed", "resetsAt":"…"}

// mid SDKs: wrapped under system+subtype
{"type":"system", "subtype":"rate_limit_event", "rateLimitType":"five_hour", …}

// current SDKs: nested under rate_limit_info
{"type":"rate_limit_event", "rate_limit_info":{"rateLimitType":"five_hour", …}}
```

Implementation: `hub/internal/hostrunner/driver_stdio.go::translateRateLimit`.

### Windows

Anthropic ships rate limits in three windows. Terminology has drifted across SDK versions; the mobile humanizer (`lib/widgets/agent_feed.dart::_humanWindow`) folds equivalents:

| Canonical label | Source variants | Notes |
|---|---|---|
| `1h` | `1_hour`, `1_hours`, `one_hour`, `one_hours` | Per-hour token quota. |
| `5h` | `5_hour`, `5_hours`, `five_hour`, `five_hours`, `session` | Per-session token quota. |
| `weekly` | `weekly`, `week`, `7d`, `weekly_opus` | Per-week quota (newer plans). |

Other engines (codex, gemini-cli) do not emit equivalent in-stream rate-limit signals at v1.0.350-alpha. The `rate_limit` agent_event is currently claude-only. New engines that emit equivalent signals should normalize to the same `{window, status, resets_at, …}` shape via their driver's translator.

### Fields per `rate_limit` event

| Field | Type | Notes |
|---|---|---|
| `window` | string | Canonical label per the table above. |
| `status` | string | `allowed` (under quota), `allowed_warning` (close), `allowed_max` (at the cap). Engine vocabulary; pass-through. |
| `resets_at` | string \| number | When this window resets. Source format has been *all of*: ISO-8601 string, seconds-since-epoch integer, milliseconds, microseconds. The driver passes through verbatim; the mobile humanizer is responsible for parsing. See µs/ns gotcha below. |
| `overage_status` | string \| null | "Whether the user has overage enabled." Engine pass-through. |
| `overage_disabled` | bool | `true` when overage has been disabled (e.g. by org policy). |
| `is_using_overage` | bool \| null | `true` when current usage is in the overage band. |
| `reason` | string \| null | Reason text for `overage_disabled` (often empty). |

### Display

Mobile renders a compact strip per active window. Rules:

- **Window label** uses the canonical short form (`1h`, `5h`, `weekly`).
- **Reset countdown** is *next* reset relative to local time. The display targets minutes / hours; the driver's bug class (see §µs/ns gotcha) historically broke this.
- **Color** maps from `status`: green = allowed, yellow = allowed_warning, red = allowed_max. The threshold cliff also flips when `is_using_overage` is true.
- **Updates** on every new `rate_limit` event the engine emits. The widget memoizes the latest per-window event.

Implementation: `lib/widgets/agent_feed.dart` `_AgentRateLimitStrip` (and adjacent helpers).

### µs / ns / s `resets_at` gotcha (commit `857c151`, v1.0.340-alpha)

`resets_at` has shipped in seconds (most older SDKs), milliseconds (some intermediate), microseconds (current Anthropic), and ISO-8601 strings (developer preview). A naive parser assuming seconds produced "resets in 1540333567h" (175,000 years) when given microseconds.

The fix bounds the parse: any future timestamp more than `7d` out is treated as a unit mismatch and downscaled. The mobile humanizer accepts:

- ISO-8601 string → parse directly.
- Integer ≤ ~10^10 → seconds.
- Integer 10^10 to 10^13 → milliseconds.
- Integer ≥ 10^13 → microseconds.

Future SDK changes (nanoseconds?) would extend this ladder. Don't assume a single unit when adding a new engine.

### Why pass-through, not enforce

Two reasons:

1. **The vendor enforces.** Calling beyond the vendor quota produces a vendor-side error; termipod adds nothing by enforcing first.
2. **Vendor-specific.** Each engine's quota model is different. Termipod intentionally avoids encoding "Anthropic 5-hour bucket" semantics into the hub schema; the agent_event payload stays vendor-shaped, and the renderer adapts.

If a future need arises to *act* on these signals (e.g., pause a runaway agent before it tips into overage), the action belongs in a steward prompt or a (post-MVP) hub-side guardrail, not in the rate-limit display path.

---

## (b) Hub-level governance — scope, not budget

The hub does *not* enforce per-agent / per-team token-bucket quotas in MVP. The schema reserves the fields:

| Field | Schema | Status |
|---|---|---|
| `agents.budget_cents` | INTEGER, nullable | Reserved; not enforced. |
| `agents.spent_cents` | INTEGER, default 0 | Reserved; not aggregated end-to-end. |
| `events.usage_tokens_json` | JSON, nullable | Captured per turn (engine-emitted); not summed. |

The decision to defer budget-style enforcement is in [ADR-016](../decisions/016-subagent-scope-manifest.md):

> *"This is termipod's only governance line in MVP — `budget_cents` is deferred, per-tool approval gates are deferred, secret-bearing tools are deferred. **Scope-not-budget.**"*

The reasoning: budget enforcement is multi-tenant SaaS infrastructure (auth, billing, quota arithmetic, refund logic for partial turns, alerting). The personal-tool frame ([positioning §1.5](../discussions/positioning.md)) doesn't need it; the operator can read engine vendor consumption directly. Building it for MVP would be substantial work without a demo benefit.

What the hub *does* enforce:

| Limit | Where | Notes |
|---|---|---|
| MCP tool-call role gate | `mcp_authority.go` middleware | ADR-016 D6. Refuses tools outside the role's allowed set; not a token bucket. |
| Worker A2A target restriction | Same middleware | ADR-016 D4. Worker → parent steward only. |
| Self-modification guard | `templates.*` MCP handlers | ADR-016 D7. Steward can't edit own kind's template. |
| Singleton ensure-spawn | `handlers_general_steward.go` | One general steward per team. |

These are **scope** limits (what an agent is allowed to do), not **rate** limits (how often). Termipod's MVP governance is structural: an agent either has authority for an operation or doesn't, regardless of frequency.

### What the principal sees today

- **Per-agent budget badge.** The mobile UI shows a budget pill if `budget_cents` is set on the agent; styling-only. Tap shows historical token usage from the latest `turn.result`. There's no enforcement under the hood.
- **Vendor rate-limit strip.** Per (a) above. This is the load-bearing visible budget signal.
- **No team-level rollup.** The "you've used $X this week" UI doesn't exist. Future post-MVP work.

---

## Adding a vendor rate-limit signal for a new engine

When integrating an engine that emits in-stream quota signals:

1. **Find the equivalent frame** in the engine's stream protocol (claude-code: `rate_limit_event`; codex/gemini-cli: none today).
2. **Add a translator** in the engine's driver under `hub/internal/hostrunner/driver_<engine>.go`. Normalize to the canonical `rate_limit` agent_event with fields `{window, status, resets_at, …}`.
3. **Extend `_humanWindow` if needed** with the engine's window vocabulary.
4. **Test the µs/ns/s ladder** for `resets_at`. New units will surface as "resets in 175000h" if missed.

---

## Future work (post-MVP)

| Item | Trigger | Notes |
|---|---|---|
| Aggregate `usage_tokens_json` into `agents.spent_cents` | Multi-tenant or shared-budget use case | Needs a price table per model + an aggregator job. |
| Hub-side soft-quota alerts | Director feedback that overage surprises them | Hook into the rate-limit strip to fire an attention item at threshold. |
| Per-team weekly rollup | Multi-user team | Multi-tenant only. |
| Cross-engine quota normalization | More engines emit signals | Define a canonical "remaining capacity" vocabulary; re-spec the translator contract. |

---

## References

- [ADR-016](../decisions/016-subagent-scope-manifest.md) — scope-not-budget governance.
- [ADR-005](../decisions/005-owner-authority-model.md) — principal authority.
- [Discussion: positioning §1.5](../discussions/positioning.md) — strategic frame for why budget enforcement is deferred.
- Code: `hub/internal/hostrunner/driver_stdio.go::translateRateLimit`, `lib/widgets/agent_feed.dart::_humanWindow`, `_AgentRateLimitStrip`.
- Commits: `857c151` (µs/ns parse fix, v1.0.340-alpha).
