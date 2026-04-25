# Steward UX fixes — wedge plan

> **Companion to:** [single-agent-demo.md](single-agent-demo.md) /
> [single-agent-demo-test.md](single-agent-demo-test.md)
>
> Drives a follow-up pass on the single-agent demo after the first
> dogfood: four issues surfaced once a real steward was spawned and
> talked to from the phone. Three are small, one (the modern chat UI)
> is multi-wedge.

## Driver vs. UI: the load-bearing abstraction

Claude is one agent kind. Codex, aider, and future kinds each speak a
different protocol on stdio. The mobile UI must not learn claude's
JSON shape — it would have to relearn for every new kind.

The contract is the **typed `agent_events` row**. `StdioDriver` (and
its siblings) already does this for claude:

| Claude stream-json frame | Typed event kind | Payload schema (canonical) |
|--------------------------|------------------|----------------------------|
| `system / init`          | `session.init`   | `{session_id, model, tools}` |
| `assistant.message` text block | `text`     | `{text}` (streamed) |
| `assistant.message` tool_use block | `tool_call` | `{id, name, input}` |
| `user.message` tool_result block | `tool_result` | `{tool_use_id, content, is_error}` |
| `result`                 | `completion`     | currently the whole frame; needs normalization |
| `error`                  | `error`          | passthrough |
| `rate_limit_event`       | *(currently `raw`)* | needs new kind `rate_limit` |

**Rule:** any UI work that wants to render a richer event must first
land the typed schema in the driver. New agent kinds add a driver,
not a UI branch.

The mobile renderer dispatches on `event.kind` and, for unknown kinds,
falls back to a generic raw card (already implemented). So adding
codex-flavored events later means writing a new driver, not a new
screen.

---

## Issue 1 — Duplicate composer in `#hub-meta`

**Symptom:** Two text input boxes stack at the bottom when the steward
room renders the chat-style transcript.

**Root cause:** `lib/widgets/agent_feed.dart:190` already builds an
`AgentCompose` and includes it in the feed body (both empty-state and
populated paths). `lib/screens/team/team_channel_screen.dart:311`
(`_StewardTranscript`) wraps the feed in a Column and adds a *second*
`AgentCompose` underneath. Two layers, two boxes.

**Fix:** delete the outer composer in `_StewardTranscript`. `AgentFeed`
owns the input; the screen just hosts it.

**Files:** `lib/screens/team/team_channel_screen.dart`.

**Verify:** open `#hub-meta` with a live steward; one composer at the
bottom, no overlap.

**Effort:** minutes.

---

## Issue 2 — Steward health detection + recreate-from-phone

**Symptom:** App can't tell a healthy steward from a stuck one. Once a
steward is in `running` it shows green forever even if the underlying
claude process is wedged. There's no UI to terminate a dead steward
and re-spawn — the user has to SSH in and `pkill claude`.

This splits into three sub-wedges.

### 2a. Liveness signal

**Goal:** the Steward chip shows three states — healthy / stale / down.

**Hub change:** add `last_event_at` to the agents row response — the
timestamp of the most recent `agent_events` row for that agent. No new
column needed; the hub can `MAX(received_ts)` over agent_events at
read time, or persist it on each event-post if the join is hot enough
to matter (probably not at MVP scale).

**Mobile change:** in the AppBar Steward chip and the bootstrap
detection logic, classify by `(status, last_event_at)`:

| Status | last_event_at age | Chip state | Meaning |
|--------|-------------------|------------|---------|
| `running` | ≤2 min | green "Steward" | healthy |
| `running` | 2–10 min | amber "Steward · idle" | stale; might still be alive |
| `running` | >10 min | red "Steward · stuck" | almost certainly broken |
| `pending` | any | grey "Steward · starting" | host-runner hasn't picked it up |
| `terminated`/`failed` | any | grey "No steward" | needs recreate |

Thresholds are placeholders; tune after a few real sessions.

**Files:** `hub/internal/server/handlers_agents.go` (add field to
`agentOut`), `lib/widgets/steward_badge.dart`, `lib/screens/team/team_screen.dart`.

**Verify:** spawn steward, confirm green; SIGSTOP the claude process on
the host (`kill -STOP $(pgrep claude)`), wait 3 min, confirm amber;
SIGKILL it, confirm red within 10 min; tap the chip → recreate flow
(2b) opens.

### 2b. Recreate-from-phone

**Goal:** user taps "Recreate steward" on the chip menu → the dead
steward is torn down and the bootstrap sheet reopens.

**Hub change:** *none required.* The existing
`PATCH /v1/teams/{team}/agents/{id}` already does everything needed
when called with `{status: "terminated"}`:
1. Sets `status='terminated'` and stamps `terminated_at`.
2. Enqueues a `terminate` host command so host-runner kills the pane.
3. Records an `agent.terminate` audit row.

`HubClient.terminateAgent()` (mobile) wraps this. No new endpoint.

**Mobile change:** Steward chip tap behavior splits by liveness:
- **healthy** → opens `#hub-meta` directly (unchanged).
- **idle / stuck / starting** → opens a bottom sheet with two
  actions: *Open #hub-meta* and *Recreate steward*. Recreate confirms,
  calls `terminateAgent`, clears the per-team bootstrap-dismissed
  flag (so the auto-trigger is re-enabled), refreshes hub state,
  then routes to `showSpawnStewardSheet`.
- **none** → opens the spawn sheet directly (unchanged).

**Files:** `lib/screens/projects/projects_screen.dart`
(`_StewardChip` tap dispatch + `_showStewardActionsSheet` +
`_confirmAndRecreateSteward`).

**Verify:** with a stuck (red) steward, tap chip → bottom sheet →
*Recreate steward* → confirm → spawn sheet appears → spawn a new
one → chip flips to green.

**Effort:** ~½ day (mobile-only since the hub already supports
terminate).

### 2c. Doc fixes (free)

`docs/wedges/single-agent-demo-test.md:46` — the prereq smoke test:

```bash
echo '{"type":"user","message":{"role":"user","content":"say hi"}}' \
  | claude --print --output-format stream-json --input-format stream-json \
    --verbose --model claude-opus-4-7
```

- Add `--verbose` (the user reported this is needed for stream-json to
  produce useful output during a smoke test).
- Use the canonical model id `claude-opus-4-7`, not the short
  `opus-4-7`. Verify `claude --model opus-4-7` still works on the host
  before changing the steward template — if the short form is rejected,
  also fix `hub/templates/agents/steward.v1.yaml:36`.

**Effort:** minutes.

---

## Issue 3 — "Hosts → Add host" doesn't exist

**Symptom:** AC1 of the demo test guide ("open the team and go to
**Hosts → Add host**") references an affordance that isn't built. The
Hosts screen FAB (`lib/screens/hosts/hosts_screen.dart:146`) is
labeled `hostsAddBookmark` ("Add") and pushes `ConnectionFormScreen` —
that's the SSH-bookmark editor, not a host-runner installer.

**Gap:** there's no mobile-side path to mint a `host`-kind token and
surface the curl-piped install one-liner. W0 landed the script; the UI
to hand it to the user was assumed but never built.

### Fix: split the FAB

`hosts_screen.dart` FAB becomes a small bottom sheet on tap:
- **Add SSH bookmark** — current behavior, pushes `ConnectionFormScreen`.
- **Install host-runner** — new path:
  1. POST `/v1/auth/tokens` with `{kind: "host", scope_json: {team, role: "host"}}`.
  2. Show a screen with the install one-liner:
     ```
     curl -sSL <hub>/install.sh | bash -s -- --hub <hub> --token <plaintext>
     ```
     Copy button, share-sheet button, "I ran it" button.
  3. While the screen is open, poll `GET /v1/teams/{team}/hosts` every
     2s. When a new host row appears with this token's scope, mark
     "✓ Host registered" and auto-dismiss.

**New strings (l10n):** `hostsAddSheetTitle`, `hostsAddBookmarkAction`,
`hostsInstallRunnerAction`, `hostsInstallTitle`, `hostsInstallSubtitle`,
`hostsInstallCopy`, `hostsInstallShare`, `hostsInstallWaiting`, `hostsInstallRegistered`.

**Hub change:** the `/install.sh` route doesn't exist yet — verify
whether the W0 installer already lives at a fixed URL or whether we
need to expose it. If absent, add a static-file route on the hub that
serves `install.sh` (templated with the hub URL).

**Files:** `lib/screens/hosts/hosts_screen.dart` (FAB → sheet),
`lib/screens/hosts/install_runner_screen.dart` (NEW), `lib/services/hub/hub_client.dart` (mintHostToken),
`hub/internal/server/server.go` (install.sh route, if missing),
`docs/wedges/single-agent-demo-test.md` (rewrite AC1 step 1 to match).

**Verify:** fresh team, no hosts; tap FAB → "Install host-runner"; copy
the command, paste on a fresh VM, run; the screen flips to "✓ Host
registered" within ~30s; the host shows up in the Hosts list.

**Effort:** ~half day.

---

## Issue 4 — Modern steward chat UI

**Goal:** the steward room should look like a professional agent
console, not a stripped-down Slack channel. The data exists — claude's
stream-json is rich — but most of it is currently dropped or rendered
as opaque "raw" cards.

**Constraint:** the UI consumes typed `agent_events`, not claude JSON.
All driver-side normalization lands first; the mobile then renders by
event kind. This keeps the design portable to codex/aider/etc.

### Information surfaces (what the rich JSON gives us)

From the actual frames captured in dogfood:
- `system/init`: model, cwd, tools list, mcp_servers (with auth state),
  slash_commands, agents, skills, plugins, claude_code_version,
  output_style, permissionMode, fast_mode_state.
- `assistant.message`: streaming text + tool_use blocks; per-message
  `usage` block (input_tokens, output_tokens, cache_read_input_tokens,
  cache_creation_input_tokens, ephemeral_5m / 1h cache, service_tier,
  inference_geo).
- `rate_limit_event`: status, resetsAt, rateLimitType (5h/1h),
  overageStatus, overageDisabledReason, isUsingOverage.
- `result`: total_cost_usd, duration_ms, num_turns, terminal_reason,
  modelUsage breakdown (per-model tokens + cost + contextWindow +
  maxOutputTokens), permission_denials list, fast_mode_state.

### Driver schema additions (W-DRV)

`StdioDriver.translate` currently flattens `result` into `completion`
with the whole frame as payload. Lift the load-bearing fields into a
stable schema, then map other agent kinds onto the same shape.

| Event kind | Payload schema (proposed canonical) |
|------------|--------------------------------------|
| `session.init` | `{session_id, model, cwd, permission_mode, tools[], mcp_servers[{name,status}], slash_commands[], agents[], skills[], plugins[], version}` |
| `text` | `{text, message_id?}` (already streamed) |
| `tool_call` | `{id, name, input, started_at}` |
| `tool_result` | `{tool_use_id, content, is_error, duration_ms?}` |
| `usage` *(new)* | `{message_id, input_tokens, output_tokens, cache_read, cache_create, model, service_tier}` |
| `rate_limit` *(new)* | `{window: "5h"|"1h", status, resets_at, overage_disabled, is_using_overage}` |
| `turn.result` *(new, replaces `completion`)* | `{cost_usd, duration_ms, num_turns, terminal_reason, permission_denials[], by_model: {<model>: {input, output, cache_read, cache_create, cost_usd}}}` |
| `error` | passthrough |

`completion` stays as a deprecated alias for one release so old hubs
don't break.

Other kinds (codex, aider) implement drivers that emit the same kinds
where the source supports it; absent kinds simply don't show.

### Mobile components (W-UI)

Four sub-wedges of mobile work, each landable independently once the
driver kinds are emitted:

**W-UI-1 — Session header card.** Sticky chip at the top of the
transcript built from `session.init`. Tap to expand a drawer showing:
- Model + version + permission mode
- Tools list (groupable: builtin / mcp / plugin)
- MCP servers with auth state pills (green / "needs-auth")
- Slash commands & agents & skills
- cwd + workdir badge

**W-UI-2 — Tool-call lineage cards.** Each `tool_call` becomes an
expandable card; the matching `tool_result` (paired by `tool_use_id`)
attaches inside it. States: pending → approved / denied (from
attention_items wiring) → running → success / error. Surfaces input
args (collapsible JSON view), tool name, duration. The W2.1
permission_prompt wedge already populates the approval inline; this
sub-wedge presents it cleanly within the card's "pending" state.

**W-UI-3 — Telemetry strip.** A compact, persistent strip below the
session header:
- Running cost meter (cumulative `turn.result.cost_usd`)
- Turn token-usage chip (in/out/cache, current turn from `usage` events)
- Rate-limit progress bar (5h window from `rate_limit`; turns red as
  resets_at approaches)

**W-UI-4 — Composer enrichment.** Pull from `session.init`:
- `/`-prefix triggers slash-command picker (filter by `slash_commands`).
- `@`-prefix triggers tool / agent picker.
- File-attach already exists; keep it.

### Verify

For each agent kind shipped:
- `session.init` payload is fully populated; tools list isn't empty.
- A turn that calls a tool produces a `tool_call` event followed by a
  matching `tool_result` (paired by `tool_use_id`).
- A turn that ends produces a `turn.result` with non-zero `cost_usd`
  and `duration_ms`.
- A turn that hits the rate limit produces a `rate_limit` event whose
  `resets_at` matches what claude returns.

If the driver doesn't emit a kind, the matching mobile component
silently absents itself — no crashes, no empty cards.

### Effort

W-DRV: ~1 day (driver schema work + tests; only StdioDriver needs to
move, ACPDriver / PaneDriver fall out where applicable).
W-UI-1…4: ~2–3 days combined.

Total: multi-day, ship after issues 1–3 land.

---

## Suggested execution order

| # | Wedge | Effort | Note |
|---|-------|--------|------|
| 1 | Issue 1 — drop dup composer | minutes | Visible regression, smallest fix |
| 2 | Issue 2c — doc fixes (`--verbose`, model id) | minutes | Free |
| 3 | Issue 3 — "Install host-runner" UI | ~½ day | Unblocks AC1 |
| 4 | Issue 2a — liveness signal | ~½ day | Mobile chip + hub field |
| 5 | Issue 2b — recreate-from-phone | ~½ day | Builds on 2a |
| 6 | W-DRV — typed event schema | ~1 day | Prereq for the modern UI |
| 7 | W-UI-1 — session header | ~½ day | Easiest visual win |
| 8 | W-UI-2 — tool-call cards | ~1 day | Heart of the "professional" feel |
| 9 | W-UI-3 — telemetry strip | ~½ day | Cost / tokens / rate-limit |
| 10 | W-UI-4 — composer enrichment | ~½ day | Slash + mention pickers |

Items 1–5 land first, then driver normalization (6) before any UI work
(7–10) so future agent kinds don't need a UI rewrite to participate.
