# Antigravity engine rollout

> **Type:** plan
> **Status:** In progress (2026-05-22) — Phase 0 + Phase 1 landed; Phase 2 (W9–W11) remains
> **Audience:** contributors
> **Last verified vs code:** v1.0.640 / `agy` 1.0.1 on host
> **Implements:** [ADR-035](../decisions/035-antigravity-engine-m4-locallogtail.md)

## Implementation status (2026-05-22)

- **Phase 0 — DONE.** W1 family entry (prior commit); **W2** family-level
  mode floor (`ModeUnsupportedError` in `spawn_mode.go` → 422 + Hint;
  catches M1/M2 even on an unprobed host where the host-caps fallback is
  permissive).
- **Phase 1 — DONE.** New package
  `internal/drivers/local_log_tail/antigravity/` (pathresolver,
  watch-and-diff reader, mapper + real-transcript corpus test, sendkeys,
  adapter) + `launch_m4_antigravity.go` + the kind-gate branch in
  `runner.go` + W8 resume (`spliceAntigravityResume` + the `antigravity`
  case in `handlers_sessions.go`; the adapter posts `session.init` to
  persist the cursor). Falls back to PaneDriver M4 on any launch failure.
- **Phase 2 — code/data DONE; on-host gate pending.** **W9** permission
  handling — auto-approve is the shipped default
  (`--dangerously-skip-permissions` + steward self-gating via
  `request_approval`); the verified capture-pane *mechanism* (`CapturePane`)
  is in place, and the interactive *detector* (marker-matching agy's menu)
  is intentionally deferred to W11 because pinning agy's exact menu
  rendering requires a live interactive prompt (verify-don't-guess).
  **W10** templates — `steward.antigravity.v1.{yaml,md}` (M4-only,
  auto-approve, "use termipod dispatch not agy subagents"). **W11** is the
  on-host smoke (see "W11 runbook" below); ADR-035 stays **Proposed** until
  it passes, then flips to Accepted.

Two host-grounded refinements made while building (verify-don't-guess):

1. **Schema (W5).** Tool *results* arrive as their own typed transcript
   lines (`LIST_DIRECTORY`/`VIEW_FILE`/`MCP_TOOL`/…) carrying `content`;
   `PLANNER_RESPONSE` carries **either** `tool_calls[]` **or** `content`
   (final text); `step_index` skips values. The mapper treats any
   content-bearing non-planner/non-system type as a `tool_result` (agy's
   tool vocabulary grows), and surfaces unknown content-less types as
   drift. Corpus = the real MCP-ping transcript.
2. **MCP isolation (W7) — decision resolved → global config.** agy's
   OAuth token, store, and MCP config all live under `~/.gemini`, so a
   per-spawn `HOME` (D7 option b) would break auth. The launch path
   therefore idempotently merges a `termipod` stdio entry (`hub-mcp-bridge`
   + env, the same shape claude-code uses; agy's stdio MCP is verified
   end-to-end) into the **global** `~/.gemini/config/mcp_config.json`,
   writing `{}` not empty. The shared file holds one per-spawn token at a
   time; multi-agent attribution rides the verified
   `_meta.antigravity.google/conversation_id` correlation hook, not the
   token (Phase 2 hardening if needed). (agy *does* also support a remote
   `serverUrl` MCP entry, per its binary — kept in reserve; stdio is the
   verified path.)

**TL;DR.** Add `antigravity` (Google's `agy`) as the fifth engine,
driven by **M4 LocalLogTail** only (no M1/M2 — `agy` 1.0.1 lacks ACP and
`--output-format`). The bulk is a new per-engine adapter under
`internal/drivers/local_log_tail/antigravity/` that mirrors the
`claude_code` leaf set, with two genuinely new leaves — a
**conversationId path resolver** and a **watch-and-diff reader** (the
transcript is a rewritten snapshot, not an append log). Everything else
(send-keys, AgentEvent emission, mobile surface) is reused. `gemini-cli`
is untouched and gains a deprecation note (sunset 2026-06-18). All
engine behaviour facts were verified on host (see ADR-035); this plan
schedules the build.

## Phasing

Ordered so each phase is independently shippable and degrades
gracefully. Until the adapter (Phase 1) lands, an `antigravity` spawn
falls through to the legacy PaneDriver M4 (the `launch_m4_locallogtail.go`
fall-through), so Phase 0 is safe to ship alone.

### Phase 0 — register the engine as data (no Go adapter) (~40 LOC + YAML)

- **W1 — `agent_families.yaml` entry.** Add `family: antigravity`,
  `bin: agy`, `supports: [M4]`, `default_auth_method` (reuse the Google
  OAuth cache `~/.gemini/antigravity-oauth-token`), `prompt_*: {M4:false}`.
  No `frame_profile` (M4 LocalLogTail maps in Go, like claude-code's M4).
  Schema-locked by `agentfamilies/families_test.go`.
- **W2 — refuse M1/M2 for antigravity.** `supports: [M4]` already makes
  `spawn_mode.go` resolve only M4; add an explicit guard + `Hint` so an
  M1/M2 request returns 422 (validate-at-every-boundary) rather than
  silently coercing. Reactivation (when `agy` ships ACP/stream-json) is
  a one-line `supports` edit + a profile, no plumbing.

### Phase 1 — the LocalLogTail adapter (~450 LOC, the bulk)

New package `internal/drivers/local_log_tail/antigravity/`, mirroring
`claude_code/` (adapter, pathresolver, tailer, mapper, sendkeys,
paneresolver, state). Reuse the shared skeleton in
`local_log_tail/driver.go` where possible.

- **W3 — pathresolver (new).** Discover the agy-minted conversationId:
  read `~/.gemini/config`/`~/.gemini/antigravity-cli/cache/last_conversations.json`,
  look up the spawn workdir → conversationId; fall back to newest
  `brain/*/` by mtime for the launch race. Then wait for
  `brain/<id>/.system_generated/logs/transcript_full.jsonl`.
- **W4 — watch-and-diff reader (new).** The transcript is a rewritten
  snapshot (verified): re-read on change, key events by `step_index`,
  emit on first-sight and on `RUNNING→DONE`, last-writer-wins. The
  shared tail-from-offset reader does **not** apply — generalise the
  skeleton with a pluggable reader or fork one here.
- **W5 — mapper.** Implement the ADR-035 schema table: drop
  `USER_INPUT`/`CONVERSATION_HISTORY`; `PLANNER_RESPONSE`→`text`(+tool_call);
  `RUN_COMMAND`/`CODE_ACTION`/`VIEW_FILE`/`LIST_DIRECTORY`/`SEARCH_WEB`/
  `GENERIC`→`tool_call`/`tool_result`; `SYSTEM_MESSAGE`→`text`/drop by
  content. No delta-coalescing (events arrive whole). Lock with a
  testdata corpus (`testdata/profiles/antigravity/corpus.jsonl`) the way
  gemini's profile is corpus-tested.
- **W6 — sendkeys + paneresolver.** Reuse claude-code's approach;
  launch `agy` interactively in the pane, route turns via send-keys, keep
  the process alive across turns (one transcript spans the spawn).
- **W7 — launch path + MCP config.** Compose the antigravity adapter
  into a new `launchM4Antigravity` (or generalise `launchM4LocalLogTail`);
  extend the kind-gate at `launch_m4_locallogtail.go:88`. Write the
  `termipod` egress-proxy entry into the **global** `mcp_config.json`
  (`{"mcpServers":{...}}`) — NOT a per-workdir file. **Decide isolation
  (ADR-035 D7 a/b):** prefer a per-spawn `HOME`/config dir so the
  "global" file is effectively per-agent. Write `{}` not an empty file
  (avoids agy's parse-error-every-run bug). The hub MCP server reads
  `tools/call` `_meta.antigravity.google/conversation_id` to attribute
  calls to the spawn.
- **W8 — resume on respawn.** `engine_session_id` = conversationId;
  relaunch interactively with `--conversation <id>` (verified: appends).
  Never use headless `--conversation <id> -p` (hangs).

### Phase 2 — permissions, templates, verification (~250 LOC + YAML)

- **W9 — permission handling.** Two modes (ADR-035 D6): auto-approve
  (`--dangerously-skip-permissions`) + route higher-order decisions via
  `request_approval`; or interactive — detect the pending arrow-nav menu
  via `tmux capture-pane` (the transcript logs only the resolved
  outcome), surface an attention item, answer with send-keys arrow-nav.
- **W10 — templates.** `kind: antigravity` steward/worker prompt
  templates; discourage `agy`-native subagents (prefer termipod task
  dispatch — D-subagents). Tool-name sweep / drift-lock as for other
  engines.
- **W11 — on-host verification gate.** Smoke: spawn an antigravity agent,
  multi-turn via send-keys, confirm it reaches the hub MCP surface
  (the `ping`-style round-trip is already proven), resume across a
  respawn, exercise an approval. Then flip ADR-035 → Accepted.

  **Runbook** (must run on a host with `agy` logged in + a live hub;
  needs a real tmux, so it can't run from inside an agent session):

  1. **Auth precheck.** `agy --version` (expect 1.0.1+) and confirm
     `~/.gemini/antigravity-oauth-token` exists (run `agy` once
     interactively to populate it otherwise).
  2. **Spawn.** From the director/steward, spawn `kind: antigravity`
     using `steward.antigravity.v1` (or any template with
     `backend.kind: antigravity`). Expect the agent to reach `running`
     (not stuck `pending`) — i.e. the adapter resolved the conversation
     + transcript, not a PaneDriver fallback. Confirm via the hub log
     line `antigravity adapter started … conversation_id=…`.
  3. **Transcript ingestion.** Send a turn; confirm `text` / `tool_call`
     / `tool_result` events flow to mobile (USER_INPUT drops). A
     multi-step turn (e.g. "list this dir then read a file") should
     surface the tool_call→tool_result pairing.
  4. **MCP reach.** Have the agent call a `termipod` tool (e.g.
     `tools_list` or `projects_list`); confirm the call lands on the hub
     (audit row / response). The global `~/.gemini/config/mcp_config.json`
     should now contain a `termipod` entry alongside any pre-existing
     servers (idempotent merge; not clobbered).
  5. **Resume.** Archive→respawn (or session resume) the agent; confirm
     the new spawn's `backend.cmd` carries `--conversation <id>` and agy
     reattaches to the same conversation (transcript continues, not a
     cold start). `engine_session_id` should equal the conversationId.
  6. **Permissions (interactive, optional).** If validating the non-default
     interactive mode: spawn WITHOUT `--dangerously-skip-permissions`,
     trigger a tool gate, **capture the pane** (`tmux capture-pane -p`),
     and record the exact menu layout — that capture is the input the
     deferred interactive detector needs. Answer via the mobile picker
     (`pick_option`, arrow-nav) and confirm the menu resolves.

  On 1–5 green, flip ADR-035 → Accepted and note the W6 (interactive
  detector) follow-up if step 6 surfaced a concrete menu layout.

## Open decision (carried from ADR-035) — RESOLVED 2026-05-22

**Global-vs-per-spawn MCP config isolation (D7 a/b) → (a) global.**
Resolved during W7: option (b) per-spawn `HOME` is **not viable** because
agy's OAuth token + store + MCP config all live under `~/.gemini`, so a
per-spawn HOME breaks auth. The launch path uses one shared global
`~/.gemini/config/mcp_config.json` entry (idempotent-merged), and
attributes MCP calls to the spawn via the verified
`_meta.antigravity.google/conversation_id` hook rather than a per-spawn
token. See "Implementation status" above.

## Effort

| Phase | LOC (est.) | Gate |
|---|---|---|
| 0 — register as data | ~40 + YAML | `families_test.go`, `spawn_mode` guard |
| 1 — adapter | ~450 | mapper corpus test; adapter integration test |
| 2 — perms/templates/verify | ~250 + YAML | on-host smoke (W11) |

## References

- [ADR-035](../decisions/035-antigravity-engine-m4-locallogtail.md) — the decision + all host-verified facts.
- [ADR-027](../decisions/027-local-log-tail-driver.md) — the M4 LocalLogTail skeleton (D9 = the extension point).
- [`kimi-code-engine.md`](kimi-code-engine.md) — the most recent engine-addition plan (template for this one).
- Code: `internal/drivers/local_log_tail/claude_code/` (leaf set to mirror); `internal/hostrunner/launch_m4_locallogtail.go:88` (kind-gate); `internal/agentfamilies/agent_families.yaml` (family entries); `internal/server/spawn_mode.go` (`supports` resolution).
