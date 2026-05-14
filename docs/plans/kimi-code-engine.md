# Kimi Code CLI engine — implementation plan

> **Type:** plan
> **Status:** Shipped (W1–W7a across v1.0.575–586) — 2026-05-14
> **Audience:** contributors
> **Last verified vs code:** v1.0.579

**TL;DR.** Add `kimi-code` (Moonshot AI's "Kimi Code CLI", repo
`MoonshotAI/kimi-cli`) as the fourth engine family, driven by the
existing M1/ACP path (`ACPDriver` in `driver_acp.go`). Kimi-code only
ships an ACP daemon — no stream-json one-shot mode and no JSON-RPC
app-server — so termipod's support is **M1-only** with M4 (tmux pane)
as the sole fallback. Two kimi-specific quirks: (1) authentication is
out-of-band via `kimi login` (the daemon returns `AUTH_REQUIRED` until
login completes), and (2) Kimi has a **native built-in web search
tool** (`SearchWeb`, configured under `[services.moonshot_search]` in
`~/.kimi/config.toml`) rather than relying on MCP-provided search. We
do NOT promote `SearchWeb` frames to a typed transcript kind for v1 —
search results are voluminous and a dedicated card would clutter the
feed; they render as generic `tool_call` rows.

**Cmd shape** (director directive): `kimi --yolo --thinking acp`.
`--yolo` and `--thinking` are kimi-cli **top-level** flags that
precede the `acp` subcommand. `--yolo` auto-approves tool calls at
the engine layer — this intentionally bypasses ACP's in-stream
`session/request_permission` gate (gemini's steward omits `--yolo`
for the opposite reason). `--thinking` enables reasoning mode for
models that support it (no-op for non-thinking models).

**MCP injection**: kimi's `--mcp-config-file` is top-level, repeatable,
defaults to `~/.kimi/mcp.json` (**JSON, not TOML**, separate from
`config.toml`). W2 does a JSON read-merge-write into a per-spawn
`<workdir>/.kimi/mcp.json`, leaving the operator's `config.toml`
(and its `[services.moonshot_search]` API key) untouched.

Five wedges land the engine end-to-end. Director assumed-true on ACP
capability flags (`loadSession`, `set_mode`, `set_model`,
`prompt_image`, `prompt_pdf`) — verification happens on-host after
W4 lands. Minimum kimi-cli version: **1.43.0**.

---

## 1. Goal

After this plan:

- A `kimi-code` steward template ships built-in and can be selected
  alongside claude / codex / gemini in the steward picker.
- `kimi acp` is wired through `launchM1` → `ACPDriver` with no Go diff
  to the driver itself, only declarative additions to
  `agent_families.yaml` and a steward template.
- Kimi's native web search works (SearchWeb tool calls execute and
  return results) — rendered as a generic `tool_call` row, same as
  any other tool. No special transcript card.
- MCP config materialization preserves the operator's existing
  `~/.kimi/config.toml` (including their `[services.moonshot_search]`
  API key) by **reading and merging** the hub MCP server entry into
  a per-spawn config copy, then pointing `--mcp-config-file` at the
  merged file.
- Tester docs include the `kimi login` prerequisite + an end-to-end
  smoke step using web search.

## 2. Non-goals

- **Kimi M2 / exec-per-turn fallback.** Kimi only ships `kimi acp` —
  there is no documented stream-json one-shot mode and no JSON-RPC
  app-server. Hosts whose kimi build can't speak ACP fall through to
  M4 (tmux pane) or fail the spawn cleanly. Adding M2 support if
  Moonshot ships a headless mode later is a separate ADR.
- **Typed `web_search` transcript kind.** SearchWeb results are
  voluminous (long quoted passages, citation lists with snippets).
  A dedicated transcript card would clutter the feed. v1 renders
  SearchWeb as a generic `tool_call` row — operators can expand it
  if they want to inspect the raw output. Revisit if the generic
  row turns out to be too opaque in practice.
- **Kimi Agent SDK integration.** The Python SDK
  (`MoonshotAI/kimi-agent-sdk`) is for embedding the kimi runtime
  in third-party products; we drive the CLI via ACP instead.
- **Auto-running `kimi login` from the hub.** Login is interactive
  (browser OAuth / device code) and must run in the operator's
  shell on the host. The hub surfaces `AUTH_REQUIRED` as an
  actionable error; operators run `kimi login` once per host.
- **Per-team Moonshot API key management.** The
  `[services.moonshot_search]` `api_key` field is operator-managed
  per-host. A hub-side per-team override is post-MVP.
- **Multi-account / account-switching.** One logged-in kimi account
  per host; mirrors the gemini precedent.

## 3. Vocabulary

- **kimi-code** — family name in `agent_families.yaml`. Binary `kimi`;
  PyPI package `kimi-cli`; product marketing name "Kimi Code CLI".
  We pick `kimi-code` over `kimi-cli` to match the product marketing
  surface and the existing termipod hint text.
- **`kimi acp`** — kimi's ACP daemon subcommand. Long-running stdio
  JSON-RPC 2.0 server speaking Zed's Agent Client Protocol; same
  wire shape gemini's `gemini --acp` uses.
- **SearchWeb** — kimi's built-in web search tool name. Surfaces as
  an ACP `session/update` notification with a `tool_call` content
  block where `name == "SearchWeb"`. Backed by Moonshot's hosted
  search endpoint configured under `[services.moonshot_search]`
  (`base_url`, `api_key`).
- **AUTH_REQUIRED** — ACP error code kimi's daemon returns on
  `session/new` or `session/prompt` when no logged-in account is on
  the host. The remedy is `kimi login` run interactively in the
  operator's shell.

## 4. Surfaces affected

| Surface | Change | Wedge |
|---|---|---|
| `hub/internal/agentfamilies/agent_families.yaml` | New `family: kimi-code` entry with `supports: [M1, M4]`, `default_auth_method`, `runtime_mode_switch: {M1: rpc}`, `prompt_image: {M1: true}`, `prompt_pdf: {M1: true}` | W1 |
| `hub/internal/hostrunner/launch_m1.go` | If kimi needs a trust opt-in similar to gemini's `--skip-trust` / `GEMINI_CLI_TRUST_WORKSPACE`, add the family-specific hook here. Default assumption: no such hook needed | W1 |
| MCP-config writer (existing `writeMCPConfigForFamily`) | New `kimi-code` branch: read `~/.kimi/config.toml` if present, deep-merge `[mcp.servers.termipod]` entry, write merged result to `<workdir>/.kimi/config.toml`, splice `--mcp-config-file <path>` into cmd | W2 |
| `hub/internal/hostrunner/driver_acp.go` | No code change; existing handshake covers kimi. Verify `AUTH_REQUIRED` surfaces as `agent_event.kind=error` with operator-actionable text | W3 (verification) |
| `hub/templates/agents/steward.kimi.v1.yaml` (new) | M1-only steward template; `cmd: kimi acp`; default_role: team.coordinator | W4 |
| `hub/templates/prompts/steward.kimi.v1.md` (new) | Steward system prompt mirroring the gemini/claude variants | W4 |
| `lib/screens/projects/spawn_agent_sheet.dart` | Hint text already includes `kimi-code` — verify it stays accurate | W4 |
| `lib/screens/team/agent_families_screen.dart` | Hint text currently `kimi` — update to `kimi-code` if desired (cosmetic) | W4 |
| `docs/how-to/release-testing.md` | New scenario: spawn kimi steward, exercise SearchWeb, verify AUTH_REQUIRED path | W5 |
| `docs/how-to/test-steward-lifecycle.md` | Cross-reference the kimi variant in the engine-swap section | W5 |
| `docs/reference/engine-capabilities.md` (or equivalent) | New row/column for kimi-code: web_search=native, auth=out-of-band, mode_switch=rpc, supports M1+M4 | W5 |
| `docs/decisions/026-kimi-code-engine.md` (new) | ADR-026 — pin the M1-only decision, AUTH_REQUIRED handling, deferred SearchWeb-as-typed-kind | drafted with W1 |

## 5. Wedges

Each wedge is one commit + one version bump, mirroring the
ADR-012/013/014/021/025 cadence. Versions are placeholders pinned at
plan-write time; they'll shift if other wedges land in between.

### W1 (v1.0.575). Family row + bin probe + ADR-026 draft.

- Add the `family: kimi-code` row to `agent_families.yaml`:
  - `bin: kimi`, `version_flag: --version`
  - `supports: [M1, M4]` — no M2.
  - `default_auth_method: ""` — assumed empty (login is out-of-band,
    not via ACP `authenticate`).
  - `runtime_mode_switch: { M1: rpc }` — assumed-true that kimi
    advertises `session/set_mode` and `session/set_model`.
  - `prompt_image: { M1: true, M4: false }`, `prompt_pdf:
    { M1: true, M4: false }` — assumed-true; mobile composer will
    enable the inline image / PDF attach affordances.
  - `frame_translator: profile` with `frame_profile.rules: []` —
    ACPDriver's hand-translation covers v1 wire shape; the empty
    rules list satisfies the schema and reserves space for future
    promotions without forcing one now.
- Hint-text alignment in the two Dart screens (one already says
  `kimi-code`; the other says `kimi` — bring them in sync).
- Drafts paired ADR-026 (`docs/decisions/026-kimi-code-engine.md`)
  capturing the M1-only decision, the assumed-true capability set,
  and the deferred-SearchWeb-promotion stance.
- Tests: extend `families_test.go` to assert the row loads with
  `supports: [M1, M4]` and the assumed runtime-mode-switch shape.

### W2 (v1.0.576). MCP config: JSON read-merge-write.

- Kimi's `--mcp-config-file` flag is top-level, repeatable, defaults
  to `~/.kimi/mcp.json` (**JSON**, separate from `config.toml`). This
  is much cleaner than the TOML round-trip the prior draft assumed.
- Add a `kimi-code` branch to `writeMCPConfigForFamily`.
- Algorithm:
  1. If `~/.kimi/mcp.json` exists on the host, read it. On parse
     error, fail loud (don't silently overwrite operator's MCP).
  2. Deep-merge a single new entry into the `mcpServers` object:
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
     Existing `mcpServers.*` entries pass through unchanged.
  3. Write the merged result to `<workdir>/.kimi/mcp.json`
     (per-spawn isolation, mode `0o600`, parent `mkdir -p`).
  4. Splice `--mcp-config-file <workdir>/.kimi/mcp.json` between
     `kimi` and `--yolo` in the cmd. (Order: `kimi
     --mcp-config-file <path> --yolo --thinking acp`.)
- The operator's `[services.moonshot_search]` API key in
  `~/.kimi/config.toml` is **untouched** — we never read or write
  config.toml. No secret-copying concern.
- Tests:
  - Fresh merge: no existing `~/.kimi/mcp.json` → output contains
    only the `termipod` entry.
  - Preserve merge: existing `~/.kimi/mcp.json` with one custom MCP
    server → output contains both that server + `termipod`.
  - Error path: malformed `~/.kimi/mcp.json` → spawn fails with
    clear error pointing at the file path.
  - Teardown: per-spawn `mcp.json` removed on agent close.

### W3 (v1.0.577). Auth-required surfacing (verification).

- ACPDriver already passes JSON-RPC error responses through as
  `kind=error` agent events with payload-verbatim text. Verify on the
  real kimi binary that the daemon's AUTH_REQUIRED error message is
  operator-actionable as-shipped ("authentication required, run
  `kimi login`" or similar).
- If kimi's error string is opaque (e.g. a bare error code with no
  human text), add a kimi-code-specific rewrite in
  `driver_acp.go::translateError` that prepends "Run `kimi login` in
  your shell on this host to authenticate."
- Mobile change (optional): the existing transcript error card is
  fine; if the message is sufficiently actionable no UI change is
  needed. Defer a deep-link "open terminal on host" affordance to a
  follow-up wedge.
- Tests: a unit test against a fixture AUTH_REQUIRED JSON-RPC error
  asserts the rendered event payload contains the remediation text.

### W4 (v1.0.578). Steward template + prompt.

- New `hub/templates/agents/steward.kimi.v1.yaml`:
  ```yaml
  template: agents.steward.kimi
  version: 1
  extends: null

  driving_mode: M1
  fallback_modes: [M4]   # no M2 — kimi-code has no stream-json mode

  backend:
    kind: kimi-code
    default_workdir: ~/hub-work
    # --yolo (-y) and --thinking are kimi-cli TOP-LEVEL flags, so
    # they MUST precede the `acp` subcommand. --yolo auto-approves
    # tool calls at the engine layer; this intentionally bypasses
    # ACP's session/request_permission gate (gemini's template
    # omits --yolo for the opposite reason). --thinking enables
    # reasoning mode on models that support it (no-op otherwise).
    # launch_m1's MCP hook splices --mcp-config-file <path> right
    # after the binary name.
    cmd: "kimi --yolo --thinking acp"

  default_role: team.coordinator
  display_label: "Steward (kimi)"

  default_capabilities:
    - blob.read
    - blob.write
    - delegate
    - decision.vote: significant
    - spawn.descendants: 20
    - templates.read
    - templates.propose
    - tasks.create
    - tasks.assign_others
    - projects.create

  prompt: steward.kimi.v1.md
  ```
- New `hub/templates/prompts/steward.kimi.v1.md` mirroring the
  gemini variant verbatim. No SearchWeb-specific guidance, no
  K2.5-flavored prose — director directive: "normal prompt, no
  extra content." Prompt stays vendor-neutral and parallel to the
  other three engines' templates so the engine-swap UX feels
  consistent.
- First end-to-end smoke on the kimi host happens here. If ACP
  handshake quirks surface (auth schema mismatch, capability
  negotiation failure, unexpected `session/update` shape), iterate
  on W1's capability flags before W5 docs land.
- After smoke completes, **capture a corpus** of raw
  `session/update` notifications (including one SearchWeb turn)
  into `hub/internal/hostrunner/testdata/profiles/kimi-code/` so the
  profile parity test runs against kimi too (§7.4).

### W5 (v1.0.579). Tester docs + engine capability matrix.

- `docs/how-to/release-testing.md`: new scenario "Spawn a kimi-code
  steward" covering:
  1. Prereq: `kimi login` run once on the host
  2. Spawn the steward via the kimi-code template
  3. Send a prompt that triggers SearchWeb (e.g. "What's the latest
     stable Flutter version?") — verify the tool_call row appears
     with the search results visible on expand
  4. AUTH_REQUIRED path: spawn on a host without a logged-in
     account, verify the error card surfaces with operator-actionable
     remediation text
  5. MCP integration: ask the steward to read team state via the
     hub MCP server, verify it round-trips
- `docs/how-to/test-steward-lifecycle.md`: cross-reference kimi in
  the engine-swap section.
- `docs/reference/engine-capabilities.md` (or wherever the matrix
  lives) gets a kimi-code row: `web_search=native`, `auth=out-of-band`,
  `mode_switch=rpc`, `supports=[M1,M4]`, `prompt_image=M1`,
  `prompt_pdf=M1`.

### W6 (v1.0.584). Resume splice for kimi-code.

Shipped 2026-05-14. On-device verification surfaced that resuming a
paused kimi-code session cold-started via `session/new` instead of
`session/load`: the wire showed a fresh `sessionId` and the agent
had no memory of the prior turn. Root cause: two `switch kind`
statements in the server layer (`handleResumeSession` in
`handlers_sessions.go:553`, `respawnWithSpecMutation` in
`respawn_with_spec_mutation.go:129`) enumerated only `claude-code`
and `gemini-cli` for the resume-cursor splice. The capture path
(`captureEngineSessionID`) is engine-neutral and was already
persisting the kimi sessionId to `sessions.engine_session_id`
correctly; only the splice was gated.

Fix: add `kimi-code` to the ACP arm of both switches. `spliceACPResume`
is protocol-level (just sets `resume_session_id` on the top-level YAML
mapping) — no engine-specific behaviour needed.

- `hub/internal/server/handlers_sessions.go:553` → `case "gemini-cli", "kimi-code":`
- `hub/internal/server/respawn_with_spec_mutation.go:129` → same (defensive; kimi-code today returns at `flagForField` lookup since model/mode switching routes via RPC like gemini, but the splice belongs in the same enumeration for when ADR-021 W2.3 lands kimi).
- Test: `TestSessions_ResumeThreadsACPCursor_KimiCode` in
  `handlers_resume_engine_session_test.go` — mirrors the gemini-cli
  test verbatim. Pins both invariants: resume_session_id field
  appears on the new agent_spawns row; no `--resume` cmd flag leaks
  into the spec.

Verified on-device 2026-05-14: kimi agent's `agentCapabilities.loadSession: true`
gates the driver's session/load path; with the cursor now flowing
through, ACPDriver.Start dispatches `session/load` instead of
`session/new`, and the agent's "do you know my first turn?" answer
should change from "no" (cold-start) to citing the prior turn.

### W7 (v1.0.585). Resume picker + modelId field-name parse.

Shipped 2026-05-14. On-device verification of W6 surfaced two distinct
bugs that surfaced together when the resume actually started working:

1. **Picker hidden on resumed agent.** kimi-cli's session/load reply
   is an empty `{}` — the ACP spec lets agents omit echoing mode/model
   state on load, and kimi takes that latitude. ACPDriver.Start's
   synthetic `currentModeId`/`currentModelId` system event therefore
   never fires for the resumed agent, mobile's
   `modeModelStateFromEvents` returns null, and the picker stays
   hidden even though the daemon session is alive and routable.

   Fix: `handleResumeSession` carries the prior agent's most recent
   mode/model state event under the new agent_id via a new
   `carryModeModelStateAcrossResume` helper. The query is field-shape-
   driven (payload contains `currentModeId`/`currentModelId` or the
   available* arrays), so it works regardless of which engine wrote
   the state originally. Engine-neutral; gemini-cli echoes state on
   load anyway, so the duplicate event lands on a list mobile reduces
   to the same final state.

2. **set_model RPC silently rejected.** Kimi ships model entries with
   only the `modelId` field (ACP spec compliant); the legacy ACPDriver
   parse read `m["id"]` for both modes AND models, so kimi's
   `availableModels` cache was always empty and every `set_model`
   call was rejected as "unknown model_id" before any RPC went on the
   wire. Mobile compounded this by reading `opt['id']` in the picker
   chip too, so even when the picker rendered (cold start) every chip
   collapsed to an empty id and `id.isEmpty` silently swallowed taps.

   Fix: driver and mobile both try `modelId` first, fall back to `id`
   for backward compat with gemini-cli's loose emission. Mode entries
   keep using `id` (matches both spec and emission).

Files:
- `hub/internal/hostrunner/driver_acp.go` — modelId/id fallback in
  availableModels parse.
- `hub/internal/server/handlers_sessions.go` — call carryover after
  resume's session row UPDATE; new helper near `captureEngineSessionID`.
- `lib/widgets/session_details_sheet.dart` —
  `_ModeModelPicker._buildChip` reads modelId first.
- `lib/widgets/agent_feed.dart` — `_modeModelSig` matches.
- Tests: `TestACPDriver_SetModelDispatch_KimiShape` (driver-level
  modelId-only parse) + `TestSessions_ResumeCarriesModeModelState`
  (handler-level carryover end-to-end).

### W7a (v1.0.586). Relax set_mode/set_model gate on empty cache.

Shipped 2026-05-14. W7 fixed the carryover so mobile's picker
re-appeared on the resumed agent, BUT the driver's internal
`availableModes`/`availableModels` cache (used for id validation
before dispatching the RPC) is built from the session/new or
session/load response. Kimi's `{}` load reply leaves both caches
empty for the resumed agent. So even with a valid id from mobile's
hydrated picker, the driver pre-flight rejected:

```
input dispatch failed agent=… kind=set_model
err="acp driver: set_model unsupported (agent did not advertise models)"
```

Fix: when the cache is empty, dispatch the RPC anyway and let the
agent be the authority. The hard "unsupported" path is removed; the
"unknown id" path stays guarded by `hasList`. Rationale: mobile's
`hasMode` / `hasModel` gates already require a populated state event
before the picker even renders — so reaching the empty-cache code
path implies the carryover successfully fired, which implies the
prior agent really did advertise these ids, which implies the agent's
in-process state still knows them. Bad ids surface as JSON-RPC errors
from the agent, propagated to the operator as a snackbar instead of
silent backend rejection.

Files:
- `hub/internal/hostrunner/driver_acp.go` — set_mode + set_model
  gate relaxed.
- Test: `TestACPDriver_SetModeDispatchesWhenCacheEmpty` (replaces the
  pre-W7a `TestACPDriver_SetModeUnsupportedWhenNoList`).

### W8 (post-MVP). SearchWeb-as-typed-kind, if needed.

Deferred. If the generic `tool_call` row turns out to be too noisy
(long quoted passages collapsing the rest of the transcript), revisit
promoting SearchWeb to a typed `web_search` kind with a dedicated
collapsing card. Don't pre-build the card — measure pain first.

### W9 (post-MVP). Per-team Moonshot search API key override.

Deferred. Each host's operator manages the search API key via
`~/.kimi/config.toml`. A hub-side per-team override (write the key
into the per-spawn merged config) makes sense if a team wants
centralized billing or to disable web search for compliance reasons.

## 6. Verification

- **Wedge-local tests**: each wedge ships its own tests as noted.
- **Cross-vendor smoke (W4+)**: director has a kimi host with a
  logged-in account available — smoke testing happens in W4 rather
  than being gated behind a "future host" promise (the gating in
  ADR-012/013 slice 7).
- **Capability flags**: W1's assumed-true values for `loadSession`,
  `set_mode`, `set_model`, `prompt_image`, `prompt_pdf` are pinned
  declaratively. If on-host verification surfaces any of these as
  false, fix the family row and document the gap in W5's engine
  capability matrix in the same release.

## 7. Open questions

(All §7.1–§7.5 from the prior draft resolved. The remaining open
items below are smaller and don't gate W1.)

### 7.1. Steward prompt tone — generic or kimi-flavored?

The W4 prompt mirrors the gemini variant verbatim plus a SearchWeb
paragraph. Kimi K2.5 has distinguishing strengths (very long context,
strong Chinese-language reasoning). If the prompt should lean into
those — e.g. "feel free to keep extensive context in mind across a
long working session," or Chinese-first interaction patterns for
ZH-speaking directors — flag it during W4 review.

### 7.2. Search-tool collision with MCP search servers

If a team has an MCP-provided search server wired up (Brave, Tavily,
etc.), kimi will see BOTH `SearchWeb` and the MCP tool. We don't
arbitrate — the model picks. Fine for v1; if it produces confusion,
a per-team "disable MCP search when engine=kimi-code" toggle is a
polish wedge.

### 7.3. Version floor — **1.43.0** (resolved)

Minimum kimi-cli version: **1.43.0**. Older builds either lack the
`kimi acp` subcommand or lack the top-level `--yolo` / `--thinking`
flags this plan depends on. The W1 family row documents this in
the entry comment.

### 7.4. Frame corpus capture for the parity test

Once W4's first kimi smoke completes, capture the raw
`session/update` notifications from a representative turn (one with
a SearchWeb call) into
`hub/internal/hostrunner/testdata/profiles/kimi-code/` so the
profile parity test runs against kimi too. This is a 30-minute
follow-up once a real corpus exists; not on the W1–W5 critical path.

## 8. Rollout

- **W1** ships the family row + ADR-026 draft. Declaratively
  verifiable on CI alone (YAML loads, template parses).
- **W2** ships the read-merge-write MCP config writer. Still
  declaratively verifiable on CI.
- **W3** verifies AUTH_REQUIRED surfacing against a real kimi binary.
  First wedge that touches the live host.
- **W4** ships the steward template + prompt; first end-to-end
  smoke. Highest-risk wedge — ACP handshake quirks (auth schema
  mismatch, capability negotiation failure, set_mode/set_model
  unsupported despite assumption) surface here. If any of W1's
  assumed-true capability flags turn out to be wrong, the fix lands
  in this wedge (same release as the smoke).
- **W5** ships tester docs + the engine capability matrix entry. By
  this point the engine works end-to-end; W5 is the "ready for other
  testers" wedge.

## 9. Risks

- **Assumed-true capability flags may be wrong.** `loadSession`,
  `set_mode`, `set_model`, `prompt_image`, `prompt_pdf` are all
  declared true in W1 without on-host verification. If kimi
  advertises any as false, the mobile composer's per-engine affordance
  gating + the mode picker may misbehave on first smoke. Mitigation:
  W4 includes a verification step that reads `initialize` response
  capabilities; mismatches get fixed in the same wedge.
- **AUTH_REQUIRED UX**: out-of-band login is a step backward from
  claude's flag-time auth or codex's API-key env var. Operators may
  not realize they need `kimi login` before spawning. W5's release-
  testing scenario explicitly covers this so the message gets
  shaken out early.
- **Config round-trip strips comments**: W2's TOML merge loses any
  comments the operator wrote in `~/.kimi/config.toml`. Source-of-
  truth stays at `~/.kimi/config.toml` (we only write to the
  per-spawn workdir copy), so the operator's comments survive in
  the original — but a confused operator who edits the per-spawn
  copy expecting it to persist will be surprised. Document the
  copy-direction in W2's commit message and W5's release docs.
- **Secret-handling caveat**: the operator's Moonshot API key gets
  copied to each per-spawn workdir. Mode `0o600` matches the gemini
  precedent; the workdir itself is operator-controlled.
- **Search-tool collision** with MCP search servers (§7.2). Not
  blocking; revisit if confusion surfaces in practice.
- **No M2 fallback**: if kimi's ACP support breaks in a version
  upgrade, the engine is unusable until upstream ships a fix.
  Version floor (§7.3) lets us fail-fast at probe time.

## 10. References

- [ADR-010](../decisions/010-frame-profiles-as-data.md) — frame profile
  substrate (reserved for future kimi-code promotions; not used in v1).
- [ADR-013](../decisions/013-gemini-exec-per-turn.md) — gemini precedent
  for M1/ACP integration; kimi reuses ACPDriver wholesale.
- [ADR-021](../decisions/021-acp-capability-surface.md) — ACP
  capability negotiation (auth method, prompt_image, set_mode/set_model
  RPCs). The W1 flags are declared per its grammar.
- Moonshot AI — [`MoonshotAI/kimi-cli`](https://github.com/MoonshotAI/kimi-cli)
- Zed agent catalog — [Kimi CLI ACP entry](https://zed.dev/acp/agent/kimi-cli)
- Kimi Code docs — [`kimi acp` subcommand](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/kimi-acp.html),
  [config files](https://moonshotai.github.io/kimi-cli/en/configuration/config-files.html)
- PyPI — [`kimi-cli`](https://pypi.org/project/kimi-cli/)
